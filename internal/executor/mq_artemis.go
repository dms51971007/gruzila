package executor

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
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

var artemisConnCreateMu sync.Mutex
var artemisSubCreateMu sync.Mutex

var artemisTLSConfigCache = struct {
	mu      sync.Mutex
	configs map[string]*tls.Config
}{
	configs: make(map[string]*tls.Config),
}

const artemisPutPoolSize = 16
const artemisHeartbeat = 30 * time.Second

// Таймауты на установку TCP/TLS/STOMP: без них net.Dial и stomp.Dial могут висеть
// неограниченно при «чёрной дыре» в сети — воркеры остаются busy без роста метрик.
const artemisTCPDialTimeout = 20 * time.Second
const artemisStompHandshakeTimeout = 25 * time.Second

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

var artemisSharedReaders = struct {
	mu      sync.Mutex
	readers map[string]*artemisSharedReader
}{
	readers: make(map[string]*artemisSharedReader),
}

type artemisSharedReader struct {
	key     string
	dest    string
	sel     string
	factory mqConnectionFactory

	msgs chan *stomp.Message
	stop chan struct{}
	once sync.Once

	mu      sync.Mutex
	waiters map[string][]chan *stomp.Message
	pending map[string][]*stomp.Message
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

	dialer := net.Dialer{Timeout: artemisTCPDialTimeout}

	if m.TLSEnabled {
		tlsCfg, err := m.getOrCreateTLSConfig()
		if err != nil {
			return nil, err
		}
		raw, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("artemis tcp dial (%s): %w", addr, err)
		}
		tlsConn := tls.Client(raw, tlsCfg)
		if err := tlsConn.SetDeadline(time.Now().Add(artemisStompHandshakeTimeout)); err != nil {
			_ = tlsConn.Close()
			return nil, fmt.Errorf("artemis tls deadline (%s): %w", addr, err)
		}
		if err := tlsConn.Handshake(); err != nil {
			_ = tlsConn.Close()
			return nil, fmt.Errorf("artemis tls handshake (%s): %w", addr, err)
		}
		if err := tlsConn.SetDeadline(time.Now().Add(artemisStompHandshakeTimeout)); err != nil {
			_ = tlsConn.Close()
			return nil, err
		}
		stompConn, err := stomp.Connect(tlsConn, connOpts...)
		_ = tlsConn.SetDeadline(time.Time{})
		if err != nil {
			_ = tlsConn.Close()
			return nil, fmt.Errorf("artemis stomp connect over tls (%s): %w", addr, err)
		}
		return stompConn, nil
	}

	raw, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("artemis tcp dial (%s): %w", addr, err)
	}
	if err := raw.SetDeadline(time.Now().Add(artemisStompHandshakeTimeout)); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("artemis stomp deadline (%s): %w", addr, err)
	}
	stompConn, err := stomp.Connect(raw, connOpts...)
	_ = raw.SetDeadline(time.Time{})
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("artemis stomp connect (%s): %w", addr, err)
	}
	return stompConn, nil
}

func (m mqConnectionFactory) tlsConfigCacheKey() string {
	return "tlscfg|" + m.connCacheKey()
}

func (m mqConnectionFactory) getOrCreateTLSConfig() (*tls.Config, error) {
	key := m.tlsConfigCacheKey()
	artemisTLSConfigCache.mu.Lock()
	if cfg, ok := artemisTLSConfigCache.configs[key]; ok && cfg != nil {
		artemisTLSConfigCache.mu.Unlock()
		return cfg, nil
	}
	artemisTLSConfigCache.mu.Unlock()

	cfg, err := m.tlsConfig()
	if err != nil {
		return nil, err
	}

	artemisTLSConfigCache.mu.Lock()
	if existing, ok := artemisTLSConfigCache.configs[key]; ok && existing != nil {
		artemisTLSConfigCache.mu.Unlock()
		return existing, nil
	}
	artemisTLSConfigCache.configs[key] = cfg
	artemisTLSConfigCache.mu.Unlock()
	return cfg, nil
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

	artemisConnCreateMu.Lock()
	defer artemisConnCreateMu.Unlock()

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
		_ = conn.MustDisconnect()
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
					_ = extra.MustDisconnect()
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
	_ = conn.MustDisconnect()
	return existing, nil
}

// stompTeardown закрывает STOMP-соединение без ожидания RECEIPT от брокера.
// stomp.Disconnect() при оборванном TCP может зависнуть навсегда и заблокировать
// все воркеры, пытающиеся сбросить кэш и переподключиться.
func stompTeardown(c *stomp.Conn) {
	if c != nil {
		_ = c.MustDisconnect()
	}
}

func (m mqConnectionFactory) sharedReaderKey(dest, selector string) string {
	return m.subCacheKey(dest, selector)
}

func (m mqConnectionFactory) getOrCreateSharedReader(dest, selector string) (*artemisSharedReader, error) {
	key := m.sharedReaderKey(dest, selector)

	artemisSharedReaders.mu.Lock()
	if r, ok := artemisSharedReaders.readers[key]; ok && r != nil {
		artemisSharedReaders.mu.Unlock()
		return r, nil
	}
	artemisSharedReaders.mu.Unlock()

	r := &artemisSharedReader{
		key:     key,
		dest:    dest,
		sel:     strings.TrimSpace(selector),
		factory: m,
		msgs:    make(chan *stomp.Message, 4096),
		stop:    make(chan struct{}),
		waiters: make(map[string][]chan *stomp.Message),
		pending: make(map[string][]*stomp.Message),
	}
	go r.run()

	artemisSharedReaders.mu.Lock()
	if existing, ok := artemisSharedReaders.readers[key]; ok && existing != nil {
		artemisSharedReaders.mu.Unlock()
		r.close()
		return existing, nil
	}
	artemisSharedReaders.readers[key] = r
	artemisSharedReaders.mu.Unlock()
	return r, nil
}

func (r *artemisSharedReader) close() {
	r.once.Do(func() {
		close(r.stop)
		r.mu.Lock()
		for reqID, ws := range r.waiters {
			for _, ch := range ws {
				close(ch)
			}
			delete(r.waiters, reqID)
		}
		r.pending = make(map[string][]*stomp.Message)
		r.mu.Unlock()
	})
}

func (r *artemisSharedReader) waitForRequestID(requestID string, wait time.Duration) (*stomp.Message, error) {
	reqID := strings.TrimSpace(requestID)
	if reqID == "" {
		return nil, fmt.Errorf("empty request id")
	}

	replyCh := make(chan *stomp.Message, 1)
	r.mu.Lock()
	if pending := r.pending[reqID]; len(pending) > 0 {
		msg := pending[0]
		if len(pending) == 1 {
			delete(r.pending, reqID)
		} else {
			r.pending[reqID] = pending[1:]
		}
		r.mu.Unlock()
		return msg, nil
	}
	r.waiters[reqID] = append(r.waiters[reqID], replyCh)
	r.mu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-r.stop:
		r.removeWaiter(reqID, replyCh)
		return nil, fmt.Errorf("artemis shared reader stopped")
	case <-timer.C:
		r.removeWaiter(reqID, replyCh)
		return nil, fmt.Errorf("artemis get: no message within %v", wait)
	case msg, ok := <-replyCh:
		if !ok {
			return nil, fmt.Errorf("artemis shared reader closed")
		}
		if msg == nil {
			return nil, fmt.Errorf("artemis get: nil frame")
		}
		return msg, nil
	}
}

func (r *artemisSharedReader) removeWaiter(reqID string, target chan *stomp.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ws := r.waiters[reqID]
	if len(ws) == 0 {
		return
	}
	for i := range ws {
		if ws[i] == target {
			ws = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(ws) == 0 {
		delete(r.waiters, reqID)
		return
	}
	r.waiters[reqID] = ws
}

func artemisMsgRequestID(msg *stomp.Message) string {
	if msg == nil || msg.Header == nil {
		return ""
	}
	keys := []string{"RequestId", "requestId", "RequestID", "requestID"}
	for _, k := range keys {
		if v, ok := msg.Header.Contains(k); ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

func (r *artemisSharedReader) dispatch(msg *stomp.Message) bool {
	reqID := artemisMsgRequestID(msg)
	if reqID == "" {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if ws := r.waiters[reqID]; len(ws) > 0 {
		ch := ws[0]
		if len(ws) == 1 {
			delete(r.waiters, reqID)
		} else {
			r.waiters[reqID] = ws[1:]
		}
		ch <- msg
		return true
	}
	pending := r.pending[reqID]
	if len(pending) >= 8 {
		pending = pending[1:]
	}
	r.pending[reqID] = append(pending, msg)
	return true
}

func (r *artemisSharedReader) run() {
	for {
		select {
		case <-r.stop:
			return
		default:
		}

		conn, err := r.factory.connect()
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		var sub *stomp.Subscription
		if r.sel != "" {
			sub, err = conn.Subscribe(r.dest, stomp.AckAuto, stomp.SubscribeOpt.Header("selector", r.sel))
		} else {
			sub, err = conn.Subscribe(r.dest, stomp.AckAuto)
		}
		if err != nil {
			stompTeardown(conn)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		for {
			select {
			case <-r.stop:
				if sub != nil && sub.Active() {
					_ = sub.Unsubscribe()
				}
				stompTeardown(conn)
				return
			case msg, ok := <-sub.C:
				if !ok || msg == nil {
					if sub != nil && sub.Active() {
						_ = sub.Unsubscribe()
					}
					stompTeardown(conn)
					time.Sleep(100 * time.Millisecond)
					goto reconnect
				}
				if msg.Err != nil {
					if sub != nil && sub.Active() {
						_ = sub.Unsubscribe()
					}
					stompTeardown(conn)
					time.Sleep(100 * time.Millisecond)
					goto reconnect
				}
				if r.dispatch(msg) {
					continue
				}
				select {
				case r.msgs <- msg:
				case <-r.stop:
					if sub != nil && sub.Active() {
						_ = sub.Unsubscribe()
					}
					stompTeardown(conn)
					return
				}
			}
		}
	reconnect:
		continue
	}
}

// invalidateAllConns полностью сбрасывает все кэши соединений/подписок
// для конкретного mqConnectionFactory.
func (m mqConnectionFactory) invalidateAllConns() {
	key := m.connCacheKey()
	var shared *stomp.Conn
	var pooled []*stomp.Conn

	artemisConnCache.mu.Lock()
	if conn, ok := artemisConnCache.conns[key]; ok && conn != nil {
		delete(artemisConnCache.conns, key)
		shared = conn
	}
	artemisConnCache.mu.Unlock()

	artemisPutConnPool.mu.Lock()
	if conns, ok := artemisPutConnPool.pool[key]; ok {
		pooled = append([]*stomp.Conn(nil), conns...)
		delete(artemisPutConnPool.pool, key)
		delete(artemisPutConnPool.rr, key)
	}
	artemisPutConnPool.mu.Unlock()

	artemisTLSConfigCache.mu.Lock()
	delete(artemisTLSConfigCache.configs, m.tlsConfigCacheKey())
	artemisTLSConfigCache.mu.Unlock()

	prefix := key + "|"
	artemisSubCache.mu.Lock()
	for subKey := range artemisSubCache.subs {
		if strings.HasPrefix(subKey, prefix) {
			delete(artemisSubCache.subs, subKey)
		}
	}
	artemisSubCache.mu.Unlock()

	artemisSharedReaders.mu.Lock()
	for readerKey, r := range artemisSharedReaders.readers {
		if strings.HasPrefix(readerKey, key+"|") {
			delete(artemisSharedReaders.readers, readerKey)
			if r != nil {
				r.close()
			}
		}
	}
	artemisSharedReaders.mu.Unlock()

	stompTeardown(shared)
	for _, c := range pooled {
		stompTeardown(c)
	}
}

// invalidateReadConn сбрасывает только shared read-connection и sub cache.
func (m mqConnectionFactory) invalidateReadConn() {
	key := m.connCacheKey()
	var shared *stomp.Conn

	artemisConnCache.mu.Lock()
	if conn, ok := artemisConnCache.conns[key]; ok && conn != nil {
		delete(artemisConnCache.conns, key)
		shared = conn
	}
	artemisConnCache.mu.Unlock()

	prefix := key + "|"
	artemisSubCache.mu.Lock()
	for subKey := range artemisSubCache.subs {
		if strings.HasPrefix(subKey, prefix) {
			delete(artemisSubCache.subs, subKey)
		}
	}
	artemisSubCache.mu.Unlock()

	artemisSharedReaders.mu.Lock()
	for readerKey, r := range artemisSharedReaders.readers {
		if strings.HasPrefix(readerKey, key+"|") {
			delete(artemisSharedReaders.readers, readerKey)
			if r != nil {
				r.close()
			}
		}
	}
	artemisSharedReaders.mu.Unlock()

	stompTeardown(shared)
}

// invalidatePutConns сбрасывает только write-side пул.
func (m mqConnectionFactory) invalidatePutConns() {
	key := m.connCacheKey()
	var pooled []*stomp.Conn

	artemisPutConnPool.mu.Lock()
	if conns, ok := artemisPutConnPool.pool[key]; ok {
		pooled = append([]*stomp.Conn(nil), conns...)
		delete(artemisPutConnPool.pool, key)
		delete(artemisPutConnPool.rr, key)
	}
	artemisPutConnPool.mu.Unlock()

	for _, c := range pooled {
		stompTeardown(c)
	}
}

// subCacheKey формирует ключ cache для подписки на destination+selector.
// Это важно: для разных selector должны быть разные subscription.
func (m mqConnectionFactory) subCacheKey(dest string, selector string) string {
	return m.connCacheKey() + "|" + dest + "|" + strings.TrimSpace(selector)
}

// releaseCachedSub убирает подписку из кэша и отписывается на брокере.
// Для request-reply с уникальным selector (RequestId) подписка одноразовая; если её
// не снимать, в кэше копятся десятки subs, и при обрыве соединения go-stomp пишет
// в log по одной строке на каждую: "Subscription … ERROR message:connection closed".
func (m mqConnectionFactory) releaseCachedSub(dest string, selector string) {
	key := m.subCacheKey(dest, selector)
	artemisSubCache.mu.Lock()
	sub, ok := artemisSubCache.subs[key]
	if ok {
		delete(artemisSubCache.subs, key)
	}
	artemisSubCache.mu.Unlock()
	if ok && sub != nil && sub.Active() {
		_ = sub.Unsubscribe()
	}
}

// dropCachedSubOnly удаляет sub из кэша без broker-side UNSUBSCRIBE.
// Нужен для shared-selector путей (например X_ServiceID) на transport-ошибках.
func (m mqConnectionFactory) dropCachedSubOnly(dest string, selector string) {
	key := m.subCacheKey(dest, selector)
	artemisSubCache.mu.Lock()
	delete(artemisSubCache.subs, key)
	artemisSubCache.mu.Unlock()
}

// getOrCreateSub возвращает кэшированную подписку для destination+selector.
// Для одинакового selector внутри executor переиспользуется один listener.
func (m mqConnectionFactory) getOrCreateSub(dest string, selector string) (*stomp.Subscription, error) {
	key := m.subCacheKey(dest, selector)
	selector = strings.TrimSpace(selector)

	artemisSubCache.mu.Lock()
	if sub, ok := artemisSubCache.subs[key]; ok && sub != nil {
		if sub.Active() {
			artemisSubCache.mu.Unlock()
			return sub, nil
		}
		delete(artemisSubCache.subs, key)
	}
	artemisSubCache.mu.Unlock()

	artemisSubCreateMu.Lock()
	defer artemisSubCreateMu.Unlock()

	artemisSubCache.mu.Lock()
	if sub, ok := artemisSubCache.subs[key]; ok && sub != nil {
		if sub.Active() {
			artemisSubCache.mu.Unlock()
			return sub, nil
		}
		delete(artemisSubCache.subs, key)
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
		// Важно: не рвём shared read-conn здесь. Под конкуренцией массовый teardown
		// запускает reconnect-storm и дополнительный churn на broker-side.
		// Ошибку отдаём вызывающему коду, который уже делает retry в пределах дедлайна.
		if selector != "" {
			return nil, fmt.Errorf("artemis subscribe %s with selector %q: %w", dest, selector, err)
		}
		return nil, fmt.Errorf("artemis subscribe %s: %w", dest, err)
	}

	artemisSubCache.mu.Lock()
	if existing, ok := artemisSubCache.subs[key]; ok && existing != nil {
		if existing.Active() {
			artemisSubCache.mu.Unlock()
			_ = sub.Unsubscribe()
			return existing, nil
		}
		delete(artemisSubCache.subs, key)
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
		// PUT-ошибка: сбрасываем write-side пул, read-side не трогаем.
		m.invalidatePutConns()
		log.Printf("[mq] send error destination=%s err=%v", dest, err)
		return fmt.Errorf("artemis send to %s: %w", dest, err)
	}
	//log.Printf("[mq] send ok destination=%s", dest)
	return nil
}

// Get ждёт сообщение из destination до указанного timeout.
// Возвращает body и headers полученного сообщения.
// На transport/subscription ошибках сбрасывает кэши для последующего reconnect.
func (m mqConnectionFactory) Get(queueName string, wait time.Duration, selector string, oneShotSelector bool, expectedRequestID string) (string, map[string]string, error) {
	dest := m.destination(queueName)
	if dest == "" {
		return "", nil, fmt.Errorf("empty artemis destination")
	}

	// Shared selector (например X_ServiceID): читаем через один долгоживущий
	// reader на executor, чтобы исключить конкурентное чтение sub.C сотнями воркеров.
	if strings.TrimSpace(selector) != "" && !oneShotSelector {
		reader, err := m.getOrCreateSharedReader(dest, selector)
		if err != nil {
			return "", nil, err
		}
		if strings.TrimSpace(expectedRequestID) != "" {
			msg, err := reader.waitForRequestID(expectedRequestID, wait)
			if err != nil {
				return "", nil, err
			}
			headers := stompHeaderToMap(msg.Header)
			return string(msg.Body), headers, nil
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				return "", nil, fmt.Errorf("artemis get: no message within %v", wait)
			case msg := <-reader.msgs:
				if msg == nil {
					continue
				}
				headers := stompHeaderToMap(msg.Header)
				return string(msg.Body), headers, nil
			}
		}
	}

	sub, err := m.getOrCreateSub(dest, selector)
	if err != nil {
		return "", nil, err
	}

	timeout := time.After(wait)
	for {
		select {
		case <-timeout:
			// Обычный timeout чтения не равен transport-error.
			// Для селекторных одноразовых reply-подписок снимаем кеш подписи,
			// чтобы не копить их между вызовами.
			if oneShotSelector {
				m.releaseCachedSub(dest, selector)
			}
			return "", nil, fmt.Errorf("artemis get: no message within %v", wait)
		case msg := <-sub.C:
			if msg == nil {
				// Subscription channel closed: сбрасываем только sub cache и пробуем
				// пересоздать подписку в рамках этого же Get без немедленного teardown shared conn.
				if oneShotSelector {
					m.releaseCachedSub(dest, selector)
				}
				if wait > 200*time.Millisecond {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				return "", nil, fmt.Errorf("artemis get: nil frame")
			}
			if msg.Err != nil {
				// Аналогично nil frame: не рвём shared conn заранее, иначе при
				// конкурентных Get получаем reconnect-storm и лавину Subscription N.
				if oneShotSelector {
					m.releaseCachedSub(dest, selector)
				}
				err := fmt.Errorf("artemis get frame error: %w", msg.Err)
				if transientArtemisGetErr(err) && wait > 200*time.Millisecond {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				return "", nil, err
			}
			headers := stompHeaderToMap(msg.Header)
			log.Printf(
				"[mq] get message destination=%s headers=%v body_len=%d",
				dest,
				headers,
				len(msg.Body),
			)
			// Селекторные подписки (request-reply с уникальным RequestId) одноразовые.
			// Для shared-подписок без selector не делаем Unsubscribe после каждого
			// сообщения, иначе под конкуренцией получаем churn и ложные nil frame.
			if oneShotSelector {
				m.releaseCachedSub(dest, selector)
			}
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
