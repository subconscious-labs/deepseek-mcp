package counter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func readSnapshot(t *testing.T, dir string) Snapshot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "counter.json"))
	require.NoError(t, err)
	var s Snapshot
	require.NoError(t, json.Unmarshal(data, &s))
	return s
}

// TestNew_CreatesFileWithTodayDate — при первом запуске создаётся counter.json
// с текущей UTC-датой и нулями.
func TestNew_CreatesFileWithTodayDate(t *testing.T) {
	dir := tmpDir(t)
	c, err := New(dir, 0)
	require.NoError(t, err)

	snap := c.Snapshot()
	today := time.Now().UTC().Format("2006-01-02")
	assert.Equal(t, today, snap.Date)
	assert.Equal(t, 0, snap.SearchRequests)
	assert.Equal(t, 0, snap.FetchRequests)
	assert.Equal(t, 0.0, snap.EstimatedCostUSD)

	// файл реально на диске
	onDisk := readSnapshot(t, dir)
	assert.Equal(t, snap, onDisk)
}

// TestIncr_Persists — инкремент пишет на диск, счётчик переживает пересоздание.
func TestIncr_Persists(t *testing.T) {
	dir := tmpDir(t)

	c, err := New(dir, 0)
	require.NoError(t, err)
	require.NoError(t, c.Incr("search"))
	require.NoError(t, c.Incr("search"))
	require.NoError(t, c.Incr("fetch"))

	// пересоздаём — должен прочитать 3 запроса
	c2, err := New(dir, 0)
	require.NoError(t, err)
	snap := c2.Snapshot()
	assert.Equal(t, 2, snap.SearchRequests)
	assert.Equal(t, 1, snap.FetchRequests)
	assert.Equal(t, 3, c2.Used())
	assert.InDelta(t, 3*costPerRequestUSD, snap.EstimatedCostUSD, 1e-9)
}

// TestIncr_UnknownKind — защита от опечаток.
func TestIncr_UnknownKind(t *testing.T) {
	c, err := New(tmpDir(t), 0)
	require.NoError(t, err)
	require.Error(t, c.Incr("frobnicate"))
}

// TestCheck_NoLimit — при limit=0 всегда пропускает.
func TestCheck_NoLimit(t *testing.T) {
	c, err := New(tmpDir(t), 0)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		retry, ok := c.Check()
		assert.True(t, ok, "iteration %d", i)
		assert.Zero(t, retry)
	}
}

// TestCheck_EnforcesLimit — при достижении лимита Check возвращает retry > 0.
func TestCheck_EnforcesLimit(t *testing.T) {
	// now зафиксирован на 12:00 UTC, значит до полуночи ровно 12ч = 43200с
	fixedNow := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	c, err := NewWithClock(tmpDir(t), 2, func() time.Time { return fixedNow })
	require.NoError(t, err)

	_, ok := c.Check()
	assert.True(t, ok, "0/2 — можно")

	require.NoError(t, c.Incr("search"))
	_, ok = c.Check()
	assert.True(t, ok, "1/2 — можно")

	require.NoError(t, c.Incr("fetch"))
	retry, ok := c.Check()
	assert.False(t, ok, "2/2 — нельзя")
	assert.Equal(t, 12*3600, retry, "ровно до полуночи UTC")

	// Инкремент после лимита разрешён на уровне counter (защита на уровне Client),
	// но Check продолжает возвращать false.
	require.NoError(t, c.Incr("search"))
	_, ok = c.Check()
	assert.False(t, ok)
}

// TestCheck_BeforeLimitExhausted — граничный случай: used=limit-1 → можно.
func TestCheck_BeforeLimitExhausted(t *testing.T) {
	c, err := New(tmpDir(t), 1)
	require.NoError(t, err)

	_, ok := c.Check()
	assert.True(t, ok)

	require.NoError(t, c.Incr("search"))

	_, ok = c.Check()
	assert.False(t, ok, "1/1 — уже нельзя")
}

// TestRotation_OnDateChange — при смене UTC-даты счётчик обнуляется,
// старый снимок уходит в archive.jsonl.
func TestRotation_OnDateChange(t *testing.T) {
	dir := tmpDir(t)

	day1 := time.Date(2026, 10, 18, 23, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)

	var now = day1
	c, err := NewWithClock(dir, 0, func() time.Time { return now })
	require.NoError(t, err)

	require.NoError(t, c.Incr("search"))
	require.NoError(t, c.Incr("fetch"))
	assert.Equal(t, 2, c.Used())

	// двигаем часы
	now = day2
	require.NoError(t, c.Incr("search"))

	snap := c.Snapshot()
	assert.Equal(t, "2026-10-19", snap.Date)
	assert.Equal(t, 1, snap.SearchRequests, "новый день — начинается с 1")
	assert.Equal(t, 0, snap.FetchRequests)

	// archive.jsonl содержит старый день
	archive, err := os.ReadFile(filepath.Join(dir, "counter.archive.jsonl"))
	require.NoError(t, err)
	lines := splitLines(string(archive))
	require.Len(t, lines, 1)

	var archived Snapshot
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &archived))
	assert.Equal(t, "2026-10-18", archived.Date)
	assert.Equal(t, 1, archived.SearchRequests)
	assert.Equal(t, 1, archived.FetchRequests)
}

// TestRotation_AcrossRestart — если counter.json содержит вчерашнюю дату,
// New() при старте ротирует его.
func TestRotation_AcrossRestart(t *testing.T) {
	dir := tmpDir(t)

	// пишем «вчерашний» counter.json вручную
	yesterday := `{"date":"2026-10-18","search_requests":5,"fetch_requests":3,"estimated_cost_usd":0.006}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "counter.json"), []byte(yesterday), 0o644))

	today := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	c, err := NewWithClock(dir, 0, func() time.Time { return today })
	require.NoError(t, err)

	snap := c.Snapshot()
	assert.Equal(t, "2026-10-19", snap.Date)
	assert.Equal(t, 0, snap.SearchRequests)
	assert.Equal(t, 0, snap.FetchRequests)

	archive, err := os.ReadFile(filepath.Join(dir, "counter.archive.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(archive), `"date":"2026-10-18"`)
	assert.Contains(t, string(archive), `"search_requests":5`)
}

// TestCorruptFile_Recovers — битый counter.json не валит старт.
func TestCorruptFile_Recovers(t *testing.T) {
	dir := tmpDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "counter.json"), []byte(`{not json`), 0o644))

	c, err := New(dir, 0)
	require.NoError(t, err, "битый файл должен молча пересоздаваться")

	snap := c.Snapshot()
	assert.Equal(t, 0, snap.SearchRequests)
	assert.Equal(t, time.Now().UTC().Format("2006-01-02"), snap.Date)

	// и после пересоздания файл валидный
	onDisk := readSnapshot(t, dir)
	assert.Equal(t, snap.Date, onDisk.Date)
}

// TestNew_EmptyDirRejected — пустой dir — ошибка, а не молчаливое
// создание в текущей директории.
func TestNew_EmptyDirRejected(t *testing.T) {
	_, err := New("", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state dir is empty")
}

// TestConcurrentIncr — 100 горутин инкрементят; итог ровно 100, без гонок.
// Запускать с -race.
func TestConcurrentIncr(t *testing.T) {
	c, err := New(tmpDir(t), 0)
	require.NoError(t, err)

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			kind := "search"
			if i%2 == 0 {
				kind = "fetch"
			}
			if err := c.Incr(kind); err != nil {
				t.Errorf("Incr: %v", err)
			}
		}(i)
	}
	wg.Wait()

	snap := c.Snapshot()
	assert.Equal(t, n, snap.SearchRequests+snap.FetchRequests)
	assert.Equal(t, n/2, snap.SearchRequests)
	assert.Equal(t, n/2, snap.FetchRequests)
}

// TestAtomicWrite_NoTmpLeftBehind — после flush временный файл не остаётся.
func TestAtomicWrite_NoTmpLeftBehind(t *testing.T) {
	dir := tmpDir(t)
	c, err := New(dir, 0)
	require.NoError(t, err)
	require.NoError(t, c.Incr("search"))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp", "временный файл остался: %s", e.Name())
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if start < i {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
