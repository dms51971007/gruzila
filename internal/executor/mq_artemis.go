package executor

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-stomp/stomp"
	"github.com/go-stomp/stomp/frame"
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

// subCacheKey формирует ключ cache для подписки на destination+selector.
// Это важно: для разных selector должны быть разные subscription.
func (m mqConnectionFactory) subCacheKey(dest string, selector string) string {
	return m.connCacheKey() + "|" + dest + "|" + strings.TrimSpace(selector)
}

// getOrCreateSub возвращает подписку для destination.
// Если selector пустой — используется кэш shared-подписок.
// Если selector задан — создаётся временная подписка без кэша (ephemeral),
// чтобы не накапливать consumers при динамических selector.
func (m mqConnectionFactory) getOrCreateSub(dest string, selector string) (*stomp.Subscription, bool, error) {
	key := m.subCacheKey(dest, selector)
	selector = strings.TrimSpace(selector)

	// Dynamic selector -> always create ephemeral subscription.
	if selector != "" {
		conn, err := m.getOrCreateConn()
		if err != nil {
			return nil, false, err
		}
		sub, err := conn.Subscribe(dest, stomp.AckAuto, stomp.SubscribeOpt.Header("selector", selector))
		if err != nil {
			m.invalidateConn(conn)
			return nil, false, fmt.Errorf("artemis subscribe %s with selector %q: %w", dest, selector, err)
		}
		return sub, true, nil
	}

	artemisSubCache.mu.Lock()
	if sub, ok := artemisSubCache.subs[key]; ok && sub != nil {
		artemisSubCache.mu.Unlock()
		return sub, false, nil
	}
	artemisSubCache.mu.Unlock()

	conn, err := m.getOrCreateConn()
	if err != nil {
		return nil, false, err
	}

	var sub *stomp.Subscription
	sub, err = conn.Subscribe(dest, stomp.AckAuto)
	if err != nil {
		m.invalidateConn(conn)
		return nil, false, fmt.Errorf("artemis subscribe %s: %w", dest, err)
	}

	artemisSubCache.mu.Lock()
	if existing, ok := artemisSubCache.subs[key]; ok && existing != nil {
		artemisSubCache.mu.Unlock()
		_ = sub.Unsubscribe()
		return existing, false, nil
	}
	artemisSubCache.subs[key] = sub
	artemisSubCache.mu.Unlock()
	return sub, false, nil
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
// Дополнительные заголовки передаются через mq_headers шага.
func (m mqConnectionFactory) Put(queueName string, payload string, headers map[string]string) error {
	conn, err := m.getOrCreatePutConn()
	if err != nil {
		return err
	}

	dest := m.destination(queueName)
	if dest == "" {
		return fmt.Errorf("empty artemis destination")
	}

	// Пользовательские заголовки: stomp.SendOpt.Header делает Header.Add — дубликаты
	// (например второй content-type) брокер может игнорировать; первый остаётся от
	// createSendFrame (text/plain по умолчанию). Используем Set — перезапись, в т.ч. для
	// content-type / Content-Type и кастомных полей вроде Content.
	opts := make([]func(*frame.Frame) error, 0, len(headers))
	for k, v := range headers {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		opts = append(opts, stompSendFrameHeaderSet(key, v))
	}
	opts = append(opts, stomp.SendOpt.NoContentLength)
	if err := conn.Send(dest, "application/json", []byte(payload), opts...); err != nil {
		m.invalidateConn(conn)
		log.Printf("[mq] send error destination=%s err=%v", dest, err)
		return fmt.Errorf("artemis send to %s: %w", dest, err)
	}
	//log.Printf("[mq] send ok destination=%s", dest)
	return nil
}

// Get ждёт сообщение из destination до указанного timeout.
// Возвращает body и headers полученного сообщения.
// На transport/subscription ошибках сбрасывает кэши для последующего reconnect.
func (m mqConnectionFactory) Get(queueName string, wait time.Duration, selector string) (string, map[string]string, error) {
	dest := m.destination(queueName)
	if dest == "" {
		return "", nil, fmt.Errorf("empty artemis destination")
	}

	sub, ephemeral, err := m.getOrCreateSub(dest, selector)
	if err != nil {
		return "", nil, err
	}
	if ephemeral {
		defer func() { _ = sub.Unsubscribe() }()
	}

	timeout := time.After(wait)
	for {
		select {
		case <-timeout:
			return "", nil, fmt.Errorf("artemis get: no message within %v", wait)
		case msg := <-sub.C:
			if msg == nil {
				// Subscription channel closed: drop caches and reconnect path on next call.
				m.invalidateAllConns()
				return "", nil, fmt.Errorf("artemis get: nil frame")
			}
			if msg.Err != nil {
				m.invalidateAllConns()
				return "", nil, fmt.Errorf("artemis get frame error: %w", msg.Err)
			}
			headers := stompHeaderToMap(msg.Header)
			log.Printf(
				"[mq] get message destination=%s headers=%v body=%s",
				dest,
				headers,
				truncateForLog(string(msg.Body), 2048),
			)
			return string(msg.Body), headers, nil
		}
	}
}

// stompSendFrameHeaderSet задаёт один заголовок SEND через Set (не Add), чтобы
// значения из сценария перекрывали системные (например content-type).
func stompSendFrameHeaderSet(key, value string) func(*frame.Frame) error {
	k := key
	val := value
	return func(f *frame.Frame) error {
		if f.Command != frame.SEND {
			return fmt.Errorf("stomp: expected SEND frame, got %s", f.Command)
		}
		f.Header.Set(k, val)
		return nil
	}
}

func truncateForLog(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}

func stompHeaderToMap(h *frame.Header) map[string]string {
	if h == nil || h.Len() == 0 {
		return nil
	}
	out := make(map[string]string, h.Len())
	for i := 0; i < h.Len(); i++ {
		k, v := h.GetAt(i)
		if strings.TrimSpace(k) == "" {
			continue
		}
		out[k] = v
	}
	return out
}
