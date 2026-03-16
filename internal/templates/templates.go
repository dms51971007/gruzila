package templates

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"text/template"
)

// simple in-memory cache for parsed templates from templates/ directory

var (
	mu       sync.RWMutex
	tmpls    = make(map[string]*template.Template)
	baseDir  = "templates"
	funcs    = template.FuncMap{}
)

// SetBaseDir allows overriding templates directory (e.g. from CLI config).
func SetBaseDir(dir string) {
	mu.Lock()
	defer mu.Unlock()
	baseDir = dir
	tmpls = make(map[string]*template.Template)
}

// Render loads template by name from templates/ and executes it with data.
func Render(name string, data map[string]any) (string, error) {
	t, err := getTemplate(name)
	if err != nil {
		return "", err
	}
	b := bytesBufferPool.Get().(*bytes.Buffer)
	b.Reset()
	defer func() {
		b.Reset()
		bytesBufferPool.Put(b)
	}()
	if err := t.Execute(b, data); err != nil {
		return "", fmt.Errorf("execute template %q: %w", name, err)
	}
	return b.String(), nil
}

func getTemplate(name string) (*template.Template, error) {
	mu.RLock()
	if t, ok := tmpls[name]; ok {
		mu.RUnlock()
		return t, nil
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()
	if t, ok := tmpls[name]; ok {
		return t, nil
	}

	if baseDir == "" {
		baseDir = "templates"
	}
	path := filepath.Join(baseDir, name)
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read template %q: %w", path, err)
	}
	t, err := template.New(name).Funcs(funcs).Parse(string(content))
	if err != nil {
		return nil, fmt.Errorf("parse template %q: %w", name, err)
	}
	tmpls[name] = t
	return t, nil
}

var bytesBufferPool = sync.Pool{
	New: func() any {
		return &bytes.Buffer{}
	},
}

