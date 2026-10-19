package counter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const costPerRequestUSD = 0.00075

// Snapshot — формат counter.json. Дата — UTC, YYYY-MM-DD.
type Snapshot struct {
	Date             string  `json:"date"`
	SearchRequests   int     `json:"search_requests"`
	FetchRequests    int     `json:"fetch_requests"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

// Counter — потокобезопасный счётчик с персистентностью в STATE_DIR.
// limit=0 означает «без лимита»: счётчик всё равно ведётся, но Check всегда ok.
type Counter struct {
	mu    sync.Mutex
	dir   string
	limit int
	now   func() time.Time
	snap  Snapshot
}

// New создаёт счётчик с системными часами.
func New(dir string, limit int) (*Counter, error) {
	return newCounter(dir, limit, func() time.Time { return time.Now().UTC() })
}

// NewWithClock — для тестов; now вызывается для каждого обращения к дате.
func NewWithClock(dir string, limit int, now func() time.Time) (*Counter, error) {
	return newCounter(dir, limit, now)
}

func newCounter(dir string, limit int, now func() time.Time) (*Counter, error) {
	if dir == "" {
		return nil, errors.New("counter: state dir is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("counter: mkdir %s: %w", dir, err)
	}
	c := &Counter{dir: dir, limit: limit, now: now}
	// load вызывается из конструктора — объект ещё не опубликован,
	// поэтому мутекс не нужен. Логика ротации здесь продублирована
	// явно, без вызова rotateIfNeeded (который требует лока).
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

// Check возвращает (retryAfterSec, allowed). Если allowed=true — можно делать запрос.
// Если false — подождать retryAfterSec секунд (до ближайшей UTC-полуночи).
func (c *Counter) Check() (retryAfterSec int, allowed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.rotateIfNeeded(); err != nil {
		// Ротация упала (диск?) — не блокируем вызов; следующий Incr починит.
		return 0, true
	}
	if c.limit <= 0 {
		return 0, true
	}
	used := c.snap.SearchRequests + c.snap.FetchRequests
	if used < c.limit {
		return 0, true
	}
	return secondsUntilUTCMidnight(c.now()), false
}

// Incr увеличивает счётчик и пишет на диск. Вызывать только после успешного
// ответа от DeepSeek, чтобы неуспешные запросы не съедали дневной лимит.
func (c *Counter) Incr(kind string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.rotateIfNeeded(); err != nil {
		return err
	}
	switch kind {
	case "search":
		c.snap.SearchRequests++
	case "fetch":
		c.snap.FetchRequests++
	default:
		return fmt.Errorf("counter: unknown kind %q", kind)
	}
	c.snap.EstimatedCostUSD = float64(c.snap.SearchRequests+c.snap.FetchRequests) * costPerRequestUSD
	return c.flush()
}

// Used возвращает текущее количество запросов.
func (c *Counter) Used() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap.SearchRequests + c.snap.FetchRequests
}

// Snapshot возвращает копию текущего снимка.
func (c *Counter) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap
}

// --- внутреннее ---

func (c *Counter) path() string        { return filepath.Join(c.dir, "counter.json") }
func (c *Counter) archivePath() string { return filepath.Join(c.dir, "counter.archive.jsonl") }

// load читается ровно один раз из конструктора. Если файла нет — создаёт,
// если битый — начинает заново, если старая дата — архивирует и начинает заново.
func (c *Counter) load() error {
	today := c.now().Format("2006-01-02")
	data, err := os.ReadFile(c.path())
	if errors.Is(err, os.ErrNotExist) {
		c.snap = Snapshot{Date: today}
		return c.flush()
	}
	if err != nil {
		return fmt.Errorf("counter: read %s: %w", c.path(), err)
	}
	if err := json.Unmarshal(data, &c.snap); err != nil {
		// Битый файл не валит старт: один kill -9 не должен ломать сервис.
		c.snap = Snapshot{Date: today}
		return c.flush()
	}
	if c.snap.Date != today {
		_ = c.archiveOldSnapshot()
		c.snap = Snapshot{Date: today}
		return c.flush()
	}
	return nil
}

// rotateIfNeeded требует удержания c.mu (всегда вызывается под локом).
func (c *Counter) rotateIfNeeded() error {
	today := c.now().Format("2006-01-02")
	if c.snap.Date == today {
		return nil
	}
	if c.snap.Date != "" {
		_ = c.archiveOldSnapshot()
	}
	c.snap = Snapshot{Date: today}
	return c.flush()
}

func (c *Counter) archiveOldSnapshot() error {
	line, err := json.Marshal(c.snap)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(c.archivePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func (c *Counter) flush() error {
	data, err := json.Marshal(c.snap)
	if err != nil {
		return fmt.Errorf("counter: marshal: %w", err)
	}
	tmp := c.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("counter: write tmp: %w", err)
	}
	if err := os.Rename(tmp, c.path()); err != nil {
		return fmt.Errorf("counter: rename: %w", err)
	}
	return nil
}

func secondsUntilUTCMidnight(now time.Time) int {
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	return int(next.Sub(now).Seconds())
}
