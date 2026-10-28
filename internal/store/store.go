// Package store 保存盲盒记录和直播场次：盲盒事件按行追加到 events.jsonl，直播场次保存在 sessions.json。
// 每个直播间一个目录（data/room-<房间号>/），全部数据常驻内存，按时间排序，统计时直接切片。
package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Event 是一次盲盒开出记录。价格单位均为金瓜子（1 电池 = 100 金瓜子），且为单个价格。
type Event struct {
	ID        string `json:"id"`
	TS        int64  `json:"ts"` // 毫秒时间戳
	UID       int64  `json:"uid"`
	Uname     string `json:"uname"`
	Box       string `json:"box"`
	BoxPrice  int64  `json:"boxPrice"`
	GiftID    int64  `json:"giftId"`
	Gift      string `json:"gift"`
	GiftPrice int64  `json:"giftPrice"`
	Num       int    `json:"num"`
}

// Session 是一场直播。End 为 0 表示仍在直播。
// Offline 表示「未开播」时段：没开播时收到盲盒会自动开一段，开播或长时间没有新记录时结束。
type Session struct {
	ID      int64  `json:"id"`
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
	Title   string `json:"title"`
	Offline bool   `json:"offline,omitempty"`
}

// 开播时间相差在此范围内视为同一场（LIVE 消息时间与接口的 live_time 会有偏差）。
const sessionTolerance = 10 * 60 * 1000

// 未开播时段超过这么久没有新记录，下一条记录另起一段。
const offlineIdle = 2 * 60 * 60 * 1000

type Store struct {
	mu       sync.RWMutex
	dir      string
	events   []Event
	ids      map[string]struct{}
	sessions []Session
	evFile   *os.File
	rawFile  *os.File
	dbgFile  *os.File // 调试模式下记录所有礼物类消息
}

// Open 打开目录里的数据；dir 为空时只放在内存里、不写文件（还没设置直播间时用）。
func Open(dir string) (*Store, error) {
	if dir == "" {
		return &Store{ids: map[string]struct{}{}}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, ids: map[string]struct{}{}}
	if err := s.loadEvents(); err != nil {
		return nil, err
	}
	if err := s.loadSessions(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.evFile = f
	return s, nil
}

func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.evFile != nil {
		s.evFile.Close()
	}
	if s.rawFile != nil {
		s.rawFile.Close()
	}
	if s.dbgFile != nil {
		s.dbgFile.Close()
	}
}

func (s *Store) loadEvents() error {
	f, err := os.Open(filepath.Join(s.dir, "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.ID == "" {
			continue
		}
		if _, dup := s.ids[e.ID]; dup {
			continue
		}
		s.ids[e.ID] = struct{}{}
		s.events = append(s.events, e)
	}
	sort.SliceStable(s.events, func(i, j int) bool { return s.events[i].TS < s.events[j].TS })
	return sc.Err()
}

func (s *Store) loadSessions() error {
	b, err := os.ReadFile(filepath.Join(s.dir, "sessions.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &s.sessions); err != nil {
		log.Printf("sessions.json 损坏，已忽略：%v", err)
		s.sessions = nil
	}
	s.repairSessions()
	return nil
}

// repairSessions 按开始时间排序，并补上除最后一场外缺失的结束时间
// （旧版本在多个直播间之间切换时会留下顺序错乱、一直没结束的场次）。需持有写锁或在加载时调用。
func (s *Store) repairSessions() {
	sort.Slice(s.sessions, func(i, j int) bool { return s.sessions[i].Start < s.sessions[j].Start })
	changed := false
	for i := 0; i+1 < len(s.sessions); i++ {
		if cur := &s.sessions[i]; cur.End == 0 || cur.End > s.sessions[i+1].Start {
			cur.End = s.lastEventEnd(cur.Start, s.sessions[i+1].Start)
			changed = true
		}
	}
	if changed {
		s.saveSessions()
	}
}

// saveSessions 需在持有写锁时调用。
func (s *Store) saveSessions() {
	if s.dir == "" {
		return
	}
	b, _ := json.MarshalIndent(s.sessions, "", "  ")
	p := filepath.Join(s.dir, "sessions.json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Printf("保存场次失败：%v", err)
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		log.Printf("保存场次失败：%v", err)
	}
}

// Add 记录一个事件，重复的事件（同一 ID）返回 false。
func (s *Store) Add(e Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.ids[e.ID]; dup {
		return false
	}
	s.ids[e.ID] = struct{}{}
	n := len(s.events)
	if n == 0 || s.events[n-1].TS <= e.TS {
		s.events = append(s.events, e)
	} else {
		i := sort.Search(n, func(i int) bool { return s.events[i].TS > e.TS })
		s.events = append(s.events, Event{})
		copy(s.events[i+1:], s.events[i:])
		s.events[i] = e
	}
	if s.evFile == nil {
		return true
	}
	b, _ := json.Marshal(e)
	if _, err := s.evFile.Write(append(b, '\n')); err != nil {
		log.Printf("写入事件失败：%v", err)
	}
	return true
}

// AppendRaw 保存原始盲盒消息，便于核对字段含义。
func (s *Store) AppendRaw(body []byte) { s.appendLine(&s.rawFile, "raw-gifts.jsonl", body) }

// AppendDebug 在调试模式下记录收到的所有礼物类消息（不只是盲盒），用于排查收不到盲盒的问题。
func (s *Store) AppendDebug(body []byte) { s.appendLine(&s.dbgFile, "debug-gifts.jsonl", body) }

func (s *Store) appendLine(f **os.File, name string, body []byte) {
	if s.dir == "" {
		return
	}
	var buf bytes.Buffer
	if json.Compact(&buf, body) != nil {
		return
	}
	buf.WriteByte('\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if *f == nil {
		nf, err := os.OpenFile(filepath.Join(s.dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		*f = nf
	}
	(*f).Write(buf.Bytes())
}

// Range 返回 [from, to) 时间范围内事件的副本。
func (s *Store) Range(from, to int64) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, j := s.lowerBound(from), s.lowerBound(to)
	if i >= j {
		return nil
	}
	return append([]Event(nil), s.events[i:j]...)
}

func (s *Store) lowerBound(ts int64) int {
	return sort.Search(len(s.events), func(i int) bool { return s.events[i].TS >= ts })
}

func (s *Store) Sessions() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Session(nil), s.sessions...)
}

// CurrentSession 返回正在进行的场次，没有则返回最近一场。
func (s *Store) CurrentSession() (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n := len(s.sessions); n > 0 {
		return s.sessions[n-1], true
	}
	return Session{}, false
}

// OpenSession 返回正在进行的直播场次（不含「未开播」时段）。
func (s *Store) OpenSession() (Session, bool) {
	cur, ok := s.CurrentSession()
	return cur, ok && cur.End == 0 && !cur.Offline
}

// TouchOffline 在收到盲盒时调用：没有进行中的场次时，开一段「未开播」时段，让这些记录也能在「本场」里看到。
func (s *Store) TouchOffline(ts int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.sessions); n > 0 {
		last := &s.sessions[n-1]
		switch {
		case last.End == 0 && !last.Offline:
			return // 正在直播
		case last.End == 0: // 进行中的未开播时段
			end := s.lastEventEnd(last.Start, ts)
			if ts-end < offlineIdle {
				return
			}
			last.End = end
		case ts < last.End:
			return // 属于已结束的场次
		}
	}
	s.sessions = append(s.sessions, Session{ID: ts, Start: ts, Title: "未开播", Offline: true})
	s.saveSessions()
}

// StartSession 标记一场直播开始，返回该场次 ID。与最近一场开播时间接近时视为同一场。
func (s *Store) StartSession(start int64, title string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := len(s.sessions); n > 0 {
		last := &s.sessions[n-1]
		if !last.Offline && abs64(last.Start-start) <= sessionTolerance {
			changed := false
			if last.End != 0 {
				last.End = 0
				changed = true
			}
			if title != "" && last.Title != title {
				last.Title = title
				changed = true
			}
			if changed {
				s.saveSessions()
			}
			return last.ID
		}
		if last.End == 0 {
			// 上一场没收到下播消息（例如工具中途关闭），或者是开播前的未开播时段：以最后一条记录的时间作为结束
			last.End = min(s.lastEventEnd(last.Start, start), max(start, last.Start+1))
		}
	}
	s.sessions = append(s.sessions, Session{ID: start, Start: start, Title: title})
	s.repairSessions()
	s.saveSessions()
	return start
}

// EndSession 结束当前场次。stale 为 true 表示不确定实际下播时间，改用最后一条记录的时间。
func (s *Store) EndSession(end int64, stale bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.sessions)
	if n == 0 || s.sessions[n-1].End != 0 || s.sessions[n-1].Offline {
		return false
	}
	last := &s.sessions[n-1]
	if stale {
		end = s.lastEventEnd(last.Start, end+1)
	}
	last.End = max(end, last.Start+1)
	s.saveSessions()
	return true
}

// lastEventEnd 返回 [from, to) 内最后一条事件之后的时间，没有事件则返回 from+1。需持有锁。
func (s *Store) lastEventEnd(from, to int64) int64 {
	i, j := s.lowerBound(from), s.lowerBound(to)
	if j > i {
		return s.events[j-1].TS + 1
	}
	return from + 1
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// RoomDir 返回直播间的数据目录。
func RoomDir(dataDir string, room int64) string {
	return filepath.Join(dataDir, fmt.Sprintf("room-%d", room))
}

// MigrateLegacy 把旧版本直接放在 data/ 里的记录移到当前直播间的子目录（叠加层设置仍留在 data/）。
func MigrateLegacy(dataDir, roomDir string) {
	if _, err := os.Stat(filepath.Join(dataDir, "events.jsonl")); err != nil {
		return
	}
	if _, err := os.Stat(filepath.Join(roomDir, "events.jsonl")); err == nil {
		return // 已经有数据，不覆盖
	}
	if err := os.MkdirAll(roomDir, 0o755); err != nil {
		log.Printf("迁移数据失败：%v", err)
		return
	}
	for _, name := range []string{"events.jsonl", "sessions.json", "raw-gifts.jsonl", "debug-gifts.jsonl"} {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.Rename(src, filepath.Join(roomDir, name)); err != nil {
			log.Printf("迁移 %s 失败：%v", name, err)
		}
	}
	log.Printf("已把旧数据移到 %s", roomDir)
}
