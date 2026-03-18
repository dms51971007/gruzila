package executor

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-stomp/stomp"
)

// mqConnectionFactory реализует работу с ActiveMQ Artemis через STOMP.
type mqConnectionFactory struct {
	ConnName string
	Channel  string
	QueueMgr string
	AppUser  string
	AppPass  string
}

var artemisConnCache = struct {
	mu    sync.Mutex
	conns map[string]*stomp.Conn
}{
	conns: make(map[string]*stomp.Conn),
}

const artemisPutPoolSize = 16

// artemisPutConnPool — минимальный пул коннектов только для PUT.
// Цель: не упираться в один STOMP socket на высоком TPS.
var artemisPutConnPool = struct {
	mu   sync.Mutex
	pool map[string][]*stomp.Conn
	rr   map[string]uint64
}{
	pool: make(map[string][]*stomp.Conn),
	rr:   make(map[string]uint64),
}

var artemisSubCache = struct {
	mu   sync.Mutex
	subs map[string]*stomp.Subscription
}{
	subs: make(map[string]*stomp.Subscription),
}

// addr нормализует адрес подключения для STOMP dial.
// Поддерживается legacy-формат "host(port)" и обычный "host:port".
func (m mqConnectionFactory) addr() string {
	// Поддержка формата "host(port)" -> "host:port".
	a := strings.TrimSpace(m.ConnName)
	if strings.Contains(a, "(") && strings.HasSuffix(a, ")") {
		parts := strings.SplitN(a, "(", 2)
		host := parts[0]
		port := strings.TrimSuffix(parts[1], ")")
		return fmt.Sprintf("%s:%s", host, port)
	}
	return a
}

// connect открывает новое STOMP-соединение с Artemis.
func (m mqConnectionFactory) connect() (*stomp.Conn, error) {
	addr := m.addr()
	if addr == "" {
		return nil, fmt.Errorf("empty artemis address")
	}
	var conn *stomp.Conn
	var err error
	if strings.TrimSpace(m.AppUser) != "" {
		conn, err = stomp.Dial("tcp", addr, stomp.ConnOpt.Login(m.AppUser, m.AppPass))
	} else {
		conn, err = stomp.Dial("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("artemis connect (%s): %w", addr, err)
	}
	return conn, nil
}

// connCacheKey формирует ключ кэша соединений по endpoint/учётке.
func (m mqConnectionFactory) connCacheKey() string {
	return m.addr() + "|" + strings.TrimSpace(m.AppUser) + "|" + m.AppPass
}

// getOrCreateConn возвращает shared connection для операций чтения/подписок.
func (m mqConnectionFactory) getOrCreateConn() (*stomp.Conn, error) {
	key := m.connCacheKey()

	artemisConnCache.mu.Lock()
	if conn, ok := artemisConnCache.conns[key]; ok && conn != nil {
		artemisConnCache.mu.Unlock()
		return conn, nil
	}
	artemisConnCache.mu.Unlock()

	conn, err := m.connect()
	if err != nil {
		return nil, err
	}

	artemisConnCache.mu.Lock()
	if existing, ok := artemisConnCache.conns[key]; ok && existing != nil {
		artemisConnCache.mu.Unlock()
		_ = conn.Disconnect()
		return existing, nil
	}
	artemisConnCache.conns[key] = conn
	artemisConnCache.mu.Unlock()
	return conn, nil
}

// getOrCreatePutConn возвращает connection из round-robin пула PUT.
// Если пул ещё не прогрет, функция постепенно добирает новые коннекты
// до artemisPutPoolSize.
func (m mqConnectionFactory) getOrCreatePutConn() (*stomp.Conn, error) {
	key := m.connCacheKey()

	artemisPutConnPool.mu.Lock()
	if conns := artemisPutConnPool.pool[key]; len(conns) > 0 {
		idx := artemisPutConnPool.rr[key] % uint64(len(conns))
		artemisPutConnPool.rr[key]++
		conn := conns[idx]
		shouldGrow := len(conns) < artemisPutPoolSize
		artemisPutConnPool.mu.Unlock()
		if shouldGrow {
			if extra, err := m.connect(); err == nil {
				artemisPutConnPool.mu.Lock()
				if len(artemisPutConnPool.pool[key]) < artemisPutPoolSize {
					artemisPutConnPool.pool[key] = append(artemisPutConnPool.pool[key], extra)
					extra = nil
				}
				artemisPutConnPool.mu.Unlock()
				if extra != nil {
					_ = extra.Disconnect()
				}
			}
		}
		return conn, nil
	}
	artemisPutConnPool.mu.Unlock()

	conn, err := m.connect()
	if err != nil {
		return nil, err
	}

	artemisPutConnPool.mu.Lock()
	conns := artemisPutConnPool.pool[key]
	if len(conns) < artemisPutPoolSize {
		artemisPutConnPool.pool[key] = append(conns, conn)
		artemisPutConnPool.mu.Unlock()
		return conn, nil
	}
	// Pool already filled while we were connecting.
	idx := artemisPutConnPool.rr[key] % uint64(len(conns))
	artemisPutConnPool.rr[key]++
	existing := conns[idx]
	artemisPutConnPool.mu.Unlock()
	_ = conn.Disconnect()
	return existing, nil
}

// invalidateConn удаляет неисправное соединение из shared cache и PUT-пула,
// а также очищает связанные подписки.
// Если bad == nil, shared connection не трогается, но subscription cache
// для этого ключа всё равно очищается.
func (m mqConnectionFactory) invalidateConn(bad *stomp.Conn) {
	key := m.connCacheKey()
	artemisConnCache.mu.Lock()
	conn, ok := artemisConnCache.conns[key]
	disconnectShared := ok && conn != nil && (bad == nil || conn == bad)
	if disconnectShared {
		delete(artemisConnCache.conns, key)
	}
	artemisConnCache.mu.Unlock()
	if disconnectShared {
		_ = conn.Disconnect()
	}

	// Remove bad conn from PUT pool (if present).
	artemisPutConnPool.mu.Lock()
	if conns, ok := artemisPutConnPool.pool[key]; ok && len(conns) > 0 {
		filtered := conns[:0]
		for _, c := range conns {
			if c == bad {
				_ = c.Disconnect()
				continue
			}
			filtered = append(filtered, c)
		}
		if len(filtered) == 0 {
			delete(artemisPutConnPool.pool, key)
			delete(artemisPutConnPool.rr, key)
		} else {
			artemisPutConnPool.pool[key] = filtered
			artemisPutConnPool.rr[key] = 0
		}
	}
	artemisPutConnPool.mu.Unlock()

	// При инвалидации соединения очищаем связанные подписки.
	prefix := key + "|"
	artemisSubCache.mu.Lock()
	for subKey, sub := range artemisSubCache.subs {
		if strings.HasPrefix(subKey, prefix) {
			_ = sub.Unsubscribe()
			delete(artemisSubCache.subs, subKey)
		}
	}
	artemisSubCache.mu.Unlock()
}

// invalidateAllConns полностью сбрасывает все кэши соединений/подписок
// для конкретного mqConnectionFactory.
func (m mqConnectionFactory) invalidateAllConns() {
	key := m.connCacheKey()
	artemisConnCache.mu.Lock()
	if conn, ok := artemisConnCache.conns[key]; ok && conn != nil {
		_ = conn.Disconnect()
		delete(artemisConnCache.conns, key)
	}
	artemisConnCache.mu.Unlock()

	artemisPutConnPool.mu.Lock()
	if conns, ok := artemisPutConnPool.pool[key]; ok {
		for _, c := range conns {
			if c != nil {
				_ = c.Disconnect()
			}
		}
		delete(artemisPutConnPool.pool, key)
		delete(artemisPutConnPool.rr, key)
	}
	artemisPutConnPool.mu.Unlock()

	prefix := key + "|"
	artemisSubCache.mu.Lock()
	for subKey, sub := range artemisSubCache.subs {
		if strings.HasPrefix(subKey, prefix) {
			_ = sub.Unsubscribe()
			delete(artemisSubCache.subs, subKey)
		}
	}
	artemisSubCache.mu.Unlock()
}

// subCacheKey формирует ключ cache для подписки на destination.
func (m mqConnectionFactory) subCacheKey(dest string) string {
	return m.connCacheKey() + "|" + dest
}

// getOrCreateSub возвращает кэшированную STOMP-подписку или создаёт новую.
// Клиентский selector intentionally disabled, чтобы не создавать sticky-filter
// артефакты на брокере.
func (m mqConnectionFactory) getOrCreateSub(dest string, selector string) (*stomp.Subscription, error) {
	key := m.subCacheKey(dest)

	artemisSubCache.mu.Lock()
	if sub, ok := artemisSubCache.subs[key]; ok && sub != nil {
		artemisSubCache.mu.Unlock()
		return sub, nil
	}
	artemisSubCache.mu.Unlock()

	conn, err := m.getOrCreateConn()
	if err != nil {
		return nil, err
	}

	_ = selector // Broker selector is intentionally disabled at client side.
	sub, err := conn.Subscribe(dest, stomp.AckAuto)
	if err != nil {
		m.invalidateConn(conn)
		return nil, fmt.Errorf("artemis subscribe %s: %w", dest, err)
	}

	artemisSubCache.mu.Lock()
	if existing, ok := artemisSubCache.subs[key]; ok && existing != nil {
		artemisSubCache.mu.Unlock()
		_ = sub.Unsubscribe()
		return existing, nil
	}
	artemisSubCache.subs[key] = sub
	artemisSubCache.mu.Unlock()
	return sub, nil
}

// destination преобразует имя очереди из сценария в STOMP destination.
// Если пользователь уже передал путь с префиксом "/" — используем как есть.
func (m mqConnectionFactory) destination(queueName string) string {
	q := strings.TrimSpace(queueName)
	if q == "" {
		return ""
	}
	// Если путь уже указан полностью — используем как есть.
	if strings.HasPrefix(q, "/") {
		return q
	}
	// Никаких префиксов /queue – шлём прямо в address (topic_1/topic_2).
	return q
}

// Put отправляет JSON payload в destination через pooled STOMP connection.
func (m mqConnectionFactory) Put(queueName string, payload string) error {
	conn, err := m.getOrCreatePutConn()
	if err != nil {
		return err
	}

	dest := m.destination(queueName)
	if dest == "" {
		return fmt.Errorf("empty artemis destination")
	}

	//	log.Printf("[mq] send start destination=%s payload_size=%d", dest, len(payload))
	if err := conn.Send(dest, "application/json", []byte(payload)); err != nil {
		m.invalidateConn(conn)
		log.Printf("[mq] send error destination=%s err=%v", dest, err)
		return fmt.Errorf("artemis send to %s: %w", dest, err)
	}
	//log.Printf("[mq] send ok destination=%s", dest)
	return nil
}

// Get ждёт сообщение из destination до указанного timeout.
// На transport/subscription ошибках сбрасывает кэши для последующего reconnect.
func (m mqConnectionFactory) Get(queueName string, wait time.Duration, selector string) (string, error) {
	dest := m.destination(queueName)
	if dest == "" {
		return "", fmt.Errorf("empty artemis destination")
	}

	sub, err := m.getOrCreateSub(dest, selector)
	if err != nil {
		return "", err
	}

	timeout := time.After(wait)
	for {
		select {
		case <-timeout:
			return "", fmt.Errorf("artemis get: no message within %v", wait)
		case msg := <-sub.C:
			if msg == nil {
				// Subscription channel closed: drop caches and reconnect path on next call.
				m.invalidateAllConns()
				return "", fmt.Errorf("artemis get: nil frame")
			}
			if msg.Err != nil {
				m.invalidateAllConns()
				return "", fmt.Errorf("artemis get frame error: %w", msg.Err)
			}
			return string(msg.Body), nil
		}
	}
}
