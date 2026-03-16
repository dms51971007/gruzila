package scenario

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

var (
	ErrMissingName  = errors.New("scenario.name is required")
	ErrMissingSteps = errors.New("scenario.steps is required")
)

type Scenario struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Steps       []Step `yaml:"steps" json:"steps"`
}

type Step struct {
	Type     string            `yaml:"type" json:"type"` // rest|kafka|db|mq
	Name     string            `yaml:"name,omitempty" json:"name,omitempty"`
	Method   string            `yaml:"method,omitempty" json:"method,omitempty"`
	URL      string            `yaml:"url,omitempty" json:"url,omitempty"`
	Body     string            `yaml:"body,omitempty" json:"body,omitempty"`
	Template string            `yaml:"template,omitempty" json:"template,omitempty"`
	Headers  map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`

	// Kafka
	Topic   string   `yaml:"topic,omitempty" json:"topic,omitempty"`
	Brokers []string `yaml:"brokers,omitempty" json:"brokers,omitempty"`
	Key     string   `yaml:"key,omitempty" json:"key,omitempty"`

	// MQ (пока не реализовано)
	Queue string `yaml:"queue,omitempty" json:"queue,omitempty"`

	// DB check
	DBDSN   string `yaml:"db_dsn,omitempty" json:"db_dsn,omitempty"`
	DBQuery string `yaml:"db_query,omitempty" json:"db_query,omitempty"`

	Assert map[string]any `yaml:"assert,omitempty" json:"assert,omitempty"`
}

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

