package executor

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-stomp/stomp"
	"github.com/go-stomp/stomp/frame"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

// mqConnectionFactory реализует работу с ActiveMQ Artemis через STOMP.
type mqConnectionFactory struct {
	ConnName              string
	Channel               string
	QueueMgr              string
	AppUser               string
	AppPass               string
	TLSEnabled            bool
	TLSInsecure           bool
	TLSServerName         string
	TLSCAFile             string
	TLSCertFile           string
	TLSKeyFile            string
	TLSTrustStorePath     string
	TLSTrustStorePassword string
	TLSKeyStorePath       string
	TLSKeyStorePassword   string
	TLSCipherSuites       string
}

var artemisConnCache = struct {
	mu    sync.Mutex
	conns map[string]*stomp.Conn
}{
	conns: make(map[string]*stomp.Conn),
}

const artemisPutPoolSize = 16
const artemisHeartbeat = 30 * time.Second

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

	connOpts := make([]func(*stomp.Conn) error, 0, 1)
	connOpts = append(connOpts, stomp.ConnOpt.HeartBeat(artemisHeartbeat, artemisHeartbeat))
	if strings.TrimSpace(m.AppUser) != "" {
		connOpts = append(connOpts, stomp.ConnOpt.Login(m.AppUser, m.AppPass))
	}

	if m.TLSEnabled {
		tlsCfg, err := m.tlsConfig()
		if err != nil {
			return nil, err
		}
		netConn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("artemis tls dial (%s): %w", addr, err)
		}
		conn, err := stomp.Connect(netConn, connOpts...)
		if err != nil {
			_ = netConn.Close()
			return nil, fmt.Errorf("artemis stomp connect over tls (%s): %w", addr, err)
		}
		return conn, nil
	}

	conn, err := stomp.Dial("tcp", addr, connOpts...)
	if err != nil {
		return nil, fmt.Errorf("artemis connect (%s): %w", addr, err)
	}
	return conn, nil
}

func (m mqConnectionFactory) tlsConfig() (*tls.Config, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: m.TLSInsecure,
	}
	if strings.TrimSpace(m.TLSServerName) != "" {
		cfg.ServerName = strings.TrimSpace(m.TLSServerName)
	}

	if strings.TrimSpace(m.TLSCAFile) != "" {
		caPEM, err := os.ReadFile(strings.TrimSpace(m.TLSCAFile))
		if err != nil {
			return nil, fmt.Errorf("read mq_tls_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("parse mq_tls_ca_file: no certs found")
		}
		cfg.RootCAs = pool
	}
	if cfg.RootCAs == nil && strings.TrimSpace(m.TLSTrustStorePath) != "" {
		pool, err := loadRootCAsFromPKCS12(strings.TrimSpace(m.TLSTrustStorePath), m.TLSTrustStorePassword)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}

	certPath := strings.TrimSpace(m.TLSCertFile)
	keyPath := strings.TrimSpace(m.TLSKeyFile)
	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("both mq_tls_cert_file and mq_tls_key_file must be provided")
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load mq tls client cert/key: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if len(cfg.Certificates) == 0 && strings.TrimSpace(m.TLSKeyStorePath) != "" {
		cert, err := loadClientCertFromPKCS12(strings.TrimSpace(m.TLSKeyStorePath), m.TLSKeyStorePassword)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	if strings.TrimSpace(m.TLSCipherSuites) != "" {
		ids, err := parseTLSCipherSuites(m.TLSCipherSuites)
		if err != nil {
			return nil, err
		}
		cfg.CipherSuites = ids
		cfg.MinVersion = tls.VersionTLS12
	}

	return cfg, nil
}

// connCacheKey формирует ключ кэша соединений по endpoint/учётке.
func (m mqConnectionFactory) connCacheKey() string {
	return m.addr() + "|" +
		strings.TrimSpace(m.AppUser) + "|" + m.AppPass + "|" +
		fmt.Sprintf("tls=%t|insecure=%t|sni=%s|ca=%s|cert=%s|key=%s|truststore=%s|trustpass=%s|keystore=%s|keypass=%s|ciphers=%s",
			m.TLSEnabled,
			m.TLSInsecure,
			strings.TrimSpace(m.TLSServerName),
			strings.TrimSpace(m.TLSCAFile),
			strings.TrimSpace(m.TLSCertFile),
			strings.TrimSpace(m.TLSKeyFile),
			strings.TrimSpace(m.TLSTrustStorePath),
			m.TLSTrustStorePassword,
			strings.TrimSpace(m.TLSKeyStorePath),
			m.TLSKeyStorePassword,
			strings.TrimSpace(m.TLSCipherSuites),
		)
}

func parseTLSCipherSuites(raw string) ([]uint16, error) {
	parts := strings.Split(raw, ",")
	out := make([]uint16, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name == "" {
			continue
		}
		id, ok := tlsCipherSuiteByName(name)
		if !ok {
			return nil, fmt.Errorf("unknown mq_tls_cipher_suites value: %s", name)
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("mq_tls_cipher_suites is set but empty after parsing")
	}
	return out, nil
}

func tlsCipherSuiteByName(name string) (uint16, bool) {
	switch strings.TrimSpace(name) {
	case "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":
		return tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, true
	case "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":
		return tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384, true
	case "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":
		return tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, true
	case "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":
		return tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, true
	default:
		return 0, false
	}
}

func loadRootCAsFromPKCS12(path, password string) (*x509.CertPool, error) {
	p12, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mq_tls_truststore_path: %w", err)
	}
	certs, err := pkcs12.DecodeTrustStore(p12, password)
	if err != nil {
		// OpenSSL-generated truststore may not mark certs as "trusted".
		// Fallback to generic PKCS12 cert extraction.
		blocks, err2 := pkcs12.ToPEM(p12, password)
		if err2 != nil {
			return nil, fmt.Errorf("parse mq_tls_truststore_path (expect PKCS12 .p12/.pfx): %w", err)
		}
		certs = make([]*x509.Certificate, 0, len(blocks))
		for _, b := range blocks {
			if b == nil || b.Type != "CERTIFICATE" {
				continue
			}
			c, perr := x509.ParseCertificate(b.Bytes)
			if perr != nil {
				continue
			}
			certs = append(certs, c)
		}
	}
	pool := x509.NewCertPool()
	added := 0
	for _, cert := range certs {
		if cert == nil {
			continue
		}
		pool.AddCert(cert)
		added++
	}
	if added == 0 {
		return nil, fmt.Errorf("mq_tls_truststore_path contains no certificates")
	}
	return pool, nil
}

func loadClientCertFromPKCS12(path, password string) (tls.Certificate, error) {
	p12, err := os.ReadFile(path)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read mq_tls_keystore_path: %w", err)
	}
	privateKey, leafCert, caCerts, err := pkcs12.DecodeChain(p12, password)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse mq_tls_keystore_path (expect PKCS12 .p12/.pfx): %w", err)
	}
	if privateKey == nil || leafCert == nil {
		return tls.Certificate{}, fmt.Errorf("mq_tls_keystore_path must contain private key and certificate")
	}
	chain := make([][]byte, 0, 1+len(caCerts))
	chain = append(chain, leafCert.Raw)
	for _, c := range caCerts {
		if c == nil {
			continue
		}
		chain = append(chain, c.Raw)
	}
	return tls.Certificate{
		Certificate: chain,
		PrivateKey:  privateKey,
		Leaf:        leafCert,
	}, nil
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

// getOrCreateSub возвращает кэшированную подписку для destination+selector.
// Для одинакового selector внутри executor переиспользуется один listener.
func (m mqConnectionFactory) getOrCreateSub(dest string, selector string) (*stomp.Subscription, error) {
	key := m.subCacheKey(dest, selector)
	selector = strings.TrimSpace(selector)

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

	var sub *stomp.Subscription
	if selector != "" {
		sub, err = conn.Subscribe(dest, stomp.AckAuto, stomp.SubscribeOpt.Header("selector", selector))
	} else {
		sub, err = conn.Subscribe(dest, stomp.AckAuto)
	}
	if err != nil {
		m.invalidateConn(conn)
		if selector != "" {
			return nil, fmt.Errorf("artemis subscribe %s with selector %q: %w", dest, selector, err)
		}
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
		// После потери брокера часто деградируют сразу несколько pooled-сокетов.
		// Сбрасываем все кэши этого endpoint, чтобы следующий вызов делал чистый reconnect.
		m.invalidateAllConns()
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

	sub, err := m.getOrCreateSub(dest, selector)
	if err != nil {
		return "", nil, err
	}

	timeout := time.After(wait)
	for {
		select {
		case <-timeout:
			// Если подписка "подвисла" после сетевого разрыва, timeout сам по себе
			// не обновит cached subscription. Принудительно инвалидируем кэши,
			// чтобы следующая попытка заново подключилась к брокеру.
			m.invalidateAllConns()
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
				"[mq] get message destination=%s headers=%v body_len=%d",
				dest,
				headers,
				len(msg.Body),
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
