package templates

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"text/template"
)

// Простейший in-memory cache распарсенных шаблонов из templates/.
// Нужен, чтобы не читать и не парсить шаблон с диска на каждую итерацию.

var (
	mu      sync.RWMutex
	tmpls   = make(map[string]*template.Template)
	baseDir = "templates"
	funcs   = template.FuncMap{}
)

// SetBaseDir переопределяет базовый каталог шаблонов и сбрасывает cache.
// Полезно для тестов и нестандартной структуры проекта.
func SetBaseDir(dir string) {
	mu.Lock()
	defer mu.Unlock()
	baseDir = dir
	tmpls = make(map[string]*template.Template)
}

// Render загружает шаблон по имени (с кэшированием) и применяет data.
// Для снижения GC-нагрузки используется пул bytes.Buffer.
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

// getTemplate возвращает template.Template из cache либо читает/парсит
// файл с диска и кладёт его в cache.
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

// bytesBufferPool уменьшает аллокации при частом Render.
var bytesBufferPool = sync.Pool{
	New: func() any {
		return &bytes.Buffer{}
	},
}
