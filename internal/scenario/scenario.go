package scenario

import (
	"errors"
	"fmt"
	"os"

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
// Поля сгруппированы по типам шагов (rest/kafka/db/mq), но лежат в одной
// структуре для упрощения парсинга YAML и сериализации в API.
type Step struct {
	Type     string `yaml:"type" json:"type"` // rest|kafka|db|mq
	Name     string `yaml:"name,omitempty" json:"name,omitempty"`
	Method   string `yaml:"method,omitempty" json:"method,omitempty"`
	URL      string `yaml:"url,omitempty" json:"url,omitempty"`
	Body     string `yaml:"body,omitempty" json:"body,omitempty"`
	Template string `yaml:"template,omitempty" json:"template,omitempty"`
	// REST extract: сохранить значение из JSON-ответа в runtime-переменную.
	// Пример: rest_extract_var=omni_guid, rest_extract_path=values.omni_guid
	RestExtractVar  string `yaml:"rest_extract_var,omitempty" json:"rest_extract_var,omitempty"`
	RestExtractPath string `yaml:"rest_extract_path,omitempty" json:"rest_extract_path,omitempty"`
	// Headers: для rest — HTTP-заголовки; для mq put дополнительно сливаются с mq_headers (см. executor).
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`

	// Kafka
	Topic   string   `yaml:"topic,omitempty" json:"topic,omitempty"`
	Brokers []string `yaml:"brokers,omitempty" json:"brokers,omitempty"`
	Key     string   `yaml:"key,omitempty" json:"key,omitempty"`

	// MQ (пока не реализовано)
	Queue      string            `yaml:"queue,omitempty" json:"queue,omitempty"`
	MQAction   string            `yaml:"mq_action,omitempty" json:"mq_action,omitempty"` // put|get
	MQSelector string            `yaml:"mq_selector,omitempty" json:"mq_selector,omitempty"`
	MQHeaders  map[string]string `yaml:"mq_headers,omitempty" json:"mq_headers,omitempty"`
	MQConnName string            `yaml:"mq_conn_name,omitempty" json:"mq_conn_name,omitempty"`
	MQChannel  string            `yaml:"mq_channel,omitempty" json:"mq_channel,omitempty"`
	MQQueueMgr string            `yaml:"mq_queue_manager,omitempty" json:"mq_queue_manager,omitempty"`
	MQUser     string            `yaml:"mq_user,omitempty" json:"mq_user,omitempty"`
	MQPassword string            `yaml:"mq_password,omitempty" json:"mq_password,omitempty"`
	MQWaitMS   int               `yaml:"mq_wait_ms,omitempty" json:"mq_wait_ms,omitempty"`
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
	if err := Validate(sc); err != nil {
		return Scenario{}, err
	}
	return sc, nil
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
		default:
			return fmt.Errorf("step[%d].type must be one of: rest, kafka, db, mq", i)
		}
	}

	return nil
}
