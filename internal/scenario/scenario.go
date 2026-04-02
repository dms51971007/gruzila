package scenario

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	// ErrMissingName возвращается, если в YAML отсутствует scenario.name.
	ErrMissingName = errors.New("scenario.name is required")
	// ErrMissingSteps возвращается, если сценарий не содержит ни одного шага.
	ErrMissingSteps = errors.New("scenario.steps is required")
)

// Scenario описывает корневую структуру YAML-сценария нагрузки.
// Один Scenario состоит из последовательности шагов Step, которые
// выполняются в указанном порядке в рамках одной итерации.
type Scenario struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Steps       []Step `yaml:"steps" json:"steps"`
}

// Step описывает единичное действие сценария.
// Поля сгруппированы по типам шагов (rest/kafka/db/mq/tcp), но лежат в одной
// структуре для упрощения парсинга YAML и сериализации в API.
type Step struct {
	Type     string `yaml:"type" json:"type"` // rest|kafka|db|mq|tcp
	Name     string `yaml:"name,omitempty" json:"name,omitempty"`
	Method   string `yaml:"method,omitempty" json:"method,omitempty"`
	URL      string `yaml:"url,omitempty" json:"url,omitempty"`
	Body     string `yaml:"body,omitempty" json:"body,omitempty"`
	Template string `yaml:"template,omitempty" json:"template,omitempty"`
	// REST profile includes (relative to scenario file).
	RestProfile        string `yaml:"rest_profile,omitempty" json:"rest_profile,omitempty"`
	RestHeadersProfile string `yaml:"rest_headers_profile,omitempty" json:"rest_headers_profile,omitempty"`
	// Extract из JSON-тела ответа (rest, tcp при JSON-ответе) или сообщения (mq get).
	// Один путь: extract_var + extract_path.
	// Несколько: extract — map «имя_переменной: путь.через.точки» (пути с интерполяцией {{var}}).
	// Путь: ключи объекта через точку; индекс массива — целое (0,1,…); нестабильный порядок —
	// сегмент [field=значение] выбирает первый элемент массива-объектов с таким полем (пример:
	// data.operation.values.[id=payment.orderId].value).
	ExtractVar  string            `yaml:"extract_var,omitempty" json:"extract_var,omitempty"`
	ExtractPath string            `yaml:"extract_path,omitempty" json:"extract_path,omitempty"`
	Extract     map[string]string `yaml:"extract,omitempty" json:"extract,omitempty"`
	// Headers: для rest — HTTP-заголовки; для mq put участвуют в итоговых STOMP-заголовках (см. executor.mqHeaderSource).
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// Подключаемые профили с повторяющимися настройками.
	// Пути относительные к файлу сценария.
	MQProfile        string `yaml:"mq_profile,omitempty" json:"mq_profile,omitempty"`
	MQHeadersProfile string `yaml:"mq_headers_profile,omitempty" json:"mq_headers_profile,omitempty"`

	// Kafka
	Topic   string   `yaml:"topic,omitempty" json:"topic,omitempty"`
	Brokers []string `yaml:"brokers,omitempty" json:"brokers,omitempty"`
	Key     string   `yaml:"key,omitempty" json:"key,omitempty"`

	// MQ (пока не реализовано)
	Queue      string            `yaml:"queue,omitempty" json:"queue,omitempty"`
	MQAction   string            `yaml:"mq_action,omitempty" json:"mq_action,omitempty"` // put|get
	MQSelector string            `yaml:"mq_selector,omitempty" json:"mq_selector,omitempty"`
	MQHeaders  map[string]string `yaml:"mq_headers,omitempty" json:"mq_headers,omitempty"`
	// MQHeadersBase: при LoadFromFile — слой из mq_profile.mq_headers, затем mq_headers_profile (файл перекрывает профиль).
	// В рантайме ниже по приоритету, чем headers и mq_headers шага.
	MQHeadersBase map[string]string `yaml:"-" json:"-"`
	MQConnName    string            `yaml:"mq_conn_name,omitempty" json:"mq_conn_name,omitempty"`
	MQChannel     string            `yaml:"mq_channel,omitempty" json:"mq_channel,omitempty"`
	MQQueueMgr    string            `yaml:"mq_queue_manager,omitempty" json:"mq_queue_manager,omitempty"`
	MQUser        string            `yaml:"mq_user,omitempty" json:"mq_user,omitempty"`
	MQPassword    string            `yaml:"mq_password,omitempty" json:"mq_password,omitempty"`
	MQWaitMS      int               `yaml:"mq_wait_ms,omitempty" json:"mq_wait_ms,omitempty"`
	// MQ TLS/SSL (Artemis STOMP over TLS)
	MQTLS           bool   `yaml:"mq_tls,omitempty" json:"mq_tls,omitempty"`
	MQTLSInsecure   bool   `yaml:"mq_tls_insecure,omitempty" json:"mq_tls_insecure,omitempty"` // skip cert verification (dev only)
	MQTLSServerName string `yaml:"mq_tls_server_name,omitempty" json:"mq_tls_server_name,omitempty"`
	MQTLSCAFile     string `yaml:"mq_tls_ca_file,omitempty" json:"mq_tls_ca_file,omitempty"`
	MQTLSCertFile   string `yaml:"mq_tls_cert_file,omitempty" json:"mq_tls_cert_file,omitempty"` // optional client cert
	MQTLSKeyFile    string `yaml:"mq_tls_key_file,omitempty" json:"mq_tls_key_file,omitempty"`   // optional client key
	// Java-style PKCS12/JKS-like settings compatibility (prefer PKCS12 .p12/.pfx)
	MQTLSTrustStorePath     string `yaml:"mq_tls_truststore_path,omitempty" json:"mq_tls_truststore_path,omitempty"`
	MQTLSTrustStorePassword string `yaml:"mq_tls_truststore_password,omitempty" json:"mq_tls_truststore_password,omitempty"`
	MQTLSKeyStorePath       string `yaml:"mq_tls_keystore_path,omitempty" json:"mq_tls_keystore_path,omitempty"`
	MQTLSKeyStorePassword   string `yaml:"mq_tls_keystore_password,omitempty" json:"mq_tls_keystore_password,omitempty"`
	MQTLSCipherSuites       string `yaml:"mq_tls_cipher_suites,omitempty" json:"mq_tls_cipher_suites,omitempty"` // comma-separated Java names

	// DB check
	DBDSN   string `yaml:"db_dsn,omitempty" json:"db_dsn,omitempty"`
	DBQuery string `yaml:"db_query,omitempty" json:"db_query,omitempty"`

	// TCP (бинарные кадры: ISO 8583 и др.)
	TCPAddr              string `yaml:"tcp_addr,omitempty" json:"tcp_addr,omitempty"` // host:port
	TCPDialTimeoutMS     int    `yaml:"tcp_dial_timeout_ms,omitempty" json:"tcp_dial_timeout_ms,omitempty"`
	TCPReadTimeoutMS     int    `yaml:"tcp_read_timeout_ms,omitempty" json:"tcp_read_timeout_ms,omitempty"`
	TCPLengthPrefix      string `yaml:"tcp_length_prefix,omitempty" json:"tcp_length_prefix,omitempty"` // "" | 2be | 4be | 4ascii | 6ascii (см. executor/tcp.go)
	TCPPayload           string `yaml:"tcp_payload,omitempty" json:"tcp_payload,omitempty"`             // байты UTF-8 после подстановок
	TCPPayloadHex        string `yaml:"tcp_payload_hex,omitempty" json:"tcp_payload_hex,omitempty"`     // hex (пробелы допускаются)
	TCPReadMaxBytes      int    `yaml:"tcp_read_max_bytes,omitempty" json:"tcp_read_max_bytes,omitempty"`
	TCPAssertResponseHex string `yaml:"tcp_assert_response_hex,omitempty" json:"tcp_assert_response_hex,omitempty"` // подстрока в hex-ответе
	// TCPExtract: сырой ответ — срез байт в переменную; значение "offset:length" или "offset:length:hex"
	TCPExtract map[string]string `yaml:"tcp_extract,omitempty" json:"tcp_extract,omitempty"`
	// ISO 8583 (github.com/moov-io/iso8583): либо tcp_iso8583_fields вместо tcp_payload*, либо только unpack ответа.
	TCPISO8583Spec    string            `yaml:"tcp_iso8583_spec,omitempty" json:"tcp_iso8583_spec,omitempty"`       // spec87ascii | spec87hex | spec87track2
	TCPISO8583Fields  map[string]string `yaml:"tcp_iso8583_fields,omitempty" json:"tcp_iso8583_fields,omitempty"`   // номер поля → значение ({{var}}, {{__now:...}})
	TCPISO8583Extract map[string]string `yaml:"tcp_iso8583_extract,omitempty" json:"tcp_iso8583_extract,omitempty"` // имя переменной → номер поля ответа
	TCPISO8583Assert  map[string]string `yaml:"tcp_iso8583_assert,omitempty" json:"tcp_iso8583_assert,omitempty"`   // номер поля → ожидаемое значение
	TCPTLS            bool              `yaml:"tcp_tls,omitempty" json:"tcp_tls,omitempty"`
	TCPTLSInsecure    bool              `yaml:"tcp_tls_insecure,omitempty" json:"tcp_tls_insecure,omitempty"`
	TCPTLSServerName  string            `yaml:"tcp_tls_server_name,omitempty" json:"tcp_tls_server_name,omitempty"`

	Assert map[string]any `yaml:"assert,omitempty" json:"assert,omitempty"`
}

// LoadFromFile читает YAML-файл сценария, десериализует его в структуру
// Scenario и выполняет базовую валидацию обязательных полей.
func LoadFromFile(path string) (Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("read scenario file: %w", err)
	}

	var sc Scenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return Scenario{}, fmt.Errorf("parse yaml: %w", err)
	}
	if err := applyStepProfiles(path, &sc); err != nil {
		return Scenario{}, err
	}
	if err := Validate(sc); err != nil {
		return Scenario{}, err
	}
	return sc, nil
}

type mqProfile struct {
	MQConnName              string            `yaml:"mq_conn_name"`
	MQChannel               string            `yaml:"mq_channel"`
	MQQueueMgr              string            `yaml:"mq_queue_manager"`
	MQUser                  string            `yaml:"mq_user"`
	MQPassword              string            `yaml:"mq_password"`
	MQTLS                   bool              `yaml:"mq_tls"`
	MQTLSInsecure           bool              `yaml:"mq_tls_insecure"`
	MQTLSServerName         string            `yaml:"mq_tls_server_name"`
	MQTLSCAFile             string            `yaml:"mq_tls_ca_file"`
	MQTLSCertFile           string            `yaml:"mq_tls_cert_file"`
	MQTLSKeyFile            string            `yaml:"mq_tls_key_file"`
	MQTLSTrustStorePath     string            `yaml:"mq_tls_truststore_path"`
	MQTLSTrustStorePassword string            `yaml:"mq_tls_truststore_password"`
	MQTLSKeyStorePath       string            `yaml:"mq_tls_keystore_path"`
	MQTLSKeyStorePassword   string            `yaml:"mq_tls_keystore_password"`
	MQTLSCipherSuites       string            `yaml:"mq_tls_cipher_suites"`
	MQHeaders               map[string]string `yaml:"mq_headers"`
}

type restProfile struct {
	Method      string            `yaml:"method"`
	URL         string            `yaml:"url"`
	Body        string            `yaml:"body"`
	Template    string            `yaml:"template"`
	Headers     map[string]string `yaml:"headers"`
	ExtractVar  string            `yaml:"extract_var"`
	ExtractPath string            `yaml:"extract_path"`
	Extract     map[string]string `yaml:"extract"`
	Assert      map[string]any    `yaml:"assert"`
}

func applyStepProfiles(scenarioPath string, sc *Scenario) error {
	baseDir := filepath.Dir(scenarioPath)
	for i := range sc.Steps {
		if sc.Steps[i].RestProfile != "" {
			p, err := readRestProfile(baseDir, sc.Steps[i].RestProfile)
			if err != nil {
				return fmt.Errorf("step[%d] rest_profile: %w", i, err)
			}
			mergeRestProfile(&sc.Steps[i], p)
		}
		if sc.Steps[i].RestHeadersProfile != "" {
			h, err := readHeadersProfile(baseDir, sc.Steps[i].RestHeadersProfile, "headers")
			if err != nil {
				return fmt.Errorf("step[%d] rest_headers_profile: %w", i, err)
			}
			if len(sc.Steps[i].Headers) == 0 {
				sc.Steps[i].Headers = h
			}
		}
		if sc.Steps[i].MQProfile != "" {
			p, err := readMQProfile(baseDir, sc.Steps[i].MQProfile)
			if err != nil {
				return fmt.Errorf("step[%d] mq_profile: %w", i, err)
			}
			mergeMQProfile(&sc.Steps[i], p)
		}
		if sc.Steps[i].MQHeadersProfile != "" {
			h, err := readHeadersProfile(baseDir, sc.Steps[i].MQHeadersProfile, "mq_headers")
			if err != nil {
				return fmt.Errorf("step[%d] mq_headers_profile: %w", i, err)
			}
			if len(h) > 0 {
				if sc.Steps[i].MQHeadersBase == nil {
					sc.Steps[i].MQHeadersBase = make(map[string]string, len(h))
				}
				for k, v := range h {
					sc.Steps[i].MQHeadersBase[k] = v
				}
			}
		}
	}
	return nil
}

func readRestProfile(baseDir, relPath string) (restProfile, error) {
	path := filepath.Clean(filepath.Join(baseDir, relPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return restProfile{}, fmt.Errorf("read %q: %w", path, err)
	}
	var p restProfile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return restProfile{}, fmt.Errorf("parse %q: %w", path, err)
	}
	return p, nil
}

func readMQProfile(baseDir, relPath string) (mqProfile, error) {
	path := filepath.Clean(filepath.Join(baseDir, relPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return mqProfile{}, fmt.Errorf("read %q: %w", path, err)
	}
	var p mqProfile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return mqProfile{}, fmt.Errorf("parse %q: %w", path, err)
	}
	return p, nil
}

func readHeadersProfile(baseDir, relPath, wrappedKey string) (map[string]string, error) {
	path := filepath.Clean(filepath.Join(baseDir, relPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	var wrapped map[string]map[string]string
	if err := yaml.Unmarshal(data, &wrapped); err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}
	if m := wrapped[wrappedKey]; len(m) > 0 {
		return m, nil
	}
	var direct map[string]string
	if err := yaml.Unmarshal(data, &direct); err != nil {
		return nil, fmt.Errorf("parse %q as direct map: %w", path, err)
	}
	return direct, nil
}

// mergeStringMapInto добавляет из src в *dst только отсутствующие ключи.
func mergeStringMapInto(dst *map[string]string, src map[string]string) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]string, len(src))
	}
	for k, v := range src {
		if _, ok := (*dst)[k]; !ok {
			(*dst)[k] = v
		}
	}
}

func mergeRestProfile(st *Step, p restProfile) {
	if st.Method == "" {
		st.Method = p.Method
	}
	if st.URL == "" {
		st.URL = p.URL
	}
	if st.Body == "" {
		st.Body = p.Body
	}
	if st.Template == "" {
		st.Template = p.Template
	}
	if st.ExtractVar == "" {
		st.ExtractVar = p.ExtractVar
	}
	if st.ExtractPath == "" {
		st.ExtractPath = p.ExtractPath
	}
	mergeStringMapInto(&st.Extract, p.Extract)
	if len(st.Headers) == 0 && len(p.Headers) > 0 {
		st.Headers = p.Headers
	}
	if len(st.Assert) == 0 && len(p.Assert) > 0 {
		st.Assert = p.Assert
	}
}

func mergeMQProfile(st *Step, p mqProfile) {
	if st.MQConnName == "" {
		st.MQConnName = p.MQConnName
	}
	if st.MQChannel == "" {
		st.MQChannel = p.MQChannel
	}
	if st.MQQueueMgr == "" {
		st.MQQueueMgr = p.MQQueueMgr
	}
	if st.MQUser == "" {
		st.MQUser = p.MQUser
	}
	if st.MQPassword == "" {
		st.MQPassword = p.MQPassword
	}
	// Для bool принимаем значение профиля как базовое, если в шаге
	// не задана явная TLS-конфигурация строковыми полями.
	if !st.MQTLS && p.MQTLS {
		st.MQTLS = true
	}
	if !st.MQTLSInsecure && p.MQTLSInsecure {
		st.MQTLSInsecure = true
	}
	if st.MQTLSServerName == "" {
		st.MQTLSServerName = p.MQTLSServerName
	}
	if st.MQTLSCAFile == "" {
		st.MQTLSCAFile = p.MQTLSCAFile
	}
	if st.MQTLSCertFile == "" {
		st.MQTLSCertFile = p.MQTLSCertFile
	}
	if st.MQTLSKeyFile == "" {
		st.MQTLSKeyFile = p.MQTLSKeyFile
	}
	if st.MQTLSTrustStorePath == "" {
		st.MQTLSTrustStorePath = p.MQTLSTrustStorePath
	}
	if st.MQTLSTrustStorePassword == "" {
		st.MQTLSTrustStorePassword = p.MQTLSTrustStorePassword
	}
	if st.MQTLSKeyStorePath == "" {
		st.MQTLSKeyStorePath = p.MQTLSKeyStorePath
	}
	if st.MQTLSKeyStorePassword == "" {
		st.MQTLSKeyStorePassword = p.MQTLSKeyStorePassword
	}
	if st.MQTLSCipherSuites == "" {
		st.MQTLSCipherSuites = p.MQTLSCipherSuites
	}
	if len(p.MQHeaders) > 0 {
		if st.MQHeadersBase == nil {
			st.MQHeadersBase = make(map[string]string, len(p.MQHeaders))
		}
		for k, v := range p.MQHeaders {
			st.MQHeadersBase[k] = v
		}
	}
}

// Validate проверяет минимальную корректность сценария:
// - наличие имени и шагов;
// - обязательные поля для каждого поддерживаемого типа шага.
// Валидация намеренно остаётся "лёгкой": глубокие проверки выполняются
// непосредственно в исполнителе соответствующего шага.
func Validate(sc Scenario) error {
	if sc.Name == "" {
		return ErrMissingName
	}
	if len(sc.Steps) == 0 {
		return ErrMissingSteps
	}

	for i, st := range sc.Steps {
		switch st.Type {
		case "rest":
			if st.URL == "" {
				return fmt.Errorf("step[%d].url is required for rest", i)
			}
		case "kafka":
			if st.Topic == "" {
				return fmt.Errorf("step[%d].topic is required for kafka", i)
			}
			if len(st.Brokers) == 0 {
				return fmt.Errorf("step[%d].brokers is required for kafka", i)
			}
		case "db":
			if st.DBDSN == "" {
				return fmt.Errorf("step[%d].db_dsn is required for db", i)
			}
			if st.DBQuery == "" {
				return fmt.Errorf("step[%d].db_query is required for db", i)
			}
		case "mq":
		case "tcp":
			if st.TCPAddr == "" {
				return fmt.Errorf("step[%d].tcp_addr is required for tcp", i)
			}
			iso := len(st.TCPISO8583Fields) > 0
			raw := strings.TrimSpace(st.TCPPayload) != "" || strings.TrimSpace(st.TCPPayloadHex) != ""
			if iso && raw {
				return fmt.Errorf("step[%d] tcp: either tcp_iso8583_fields or tcp_payload/tcp_payload_hex, not both", i)
			}
			if !iso && !raw {
				return fmt.Errorf("step[%d] tcp: set tcp_iso8583_fields or tcp_payload or tcp_payload_hex", i)
			}
		default:
			return fmt.Errorf("step[%d].type must be one of: rest, kafka, db, mq, tcp", i)
		}
	}

	return nil
}
