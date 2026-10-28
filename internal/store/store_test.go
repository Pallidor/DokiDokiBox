package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddDedup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e := Event{ID: "a", TS: 2000, Uname: "x", Box: "心动盲盒", BoxPrice: 15000, Gift: "棉花糖", GiftPrice: 9000, Num: 1}
	if !s.Add(e) || s.Add(e) {
		t.Fatal("同一 ID 只应记录一次")
	}
	s.Add(Event{ID: "b", TS: 1000})
	if evs := s.Range(0, 1<<62); len(evs) != 2 || evs[0].ID != "b" {
		t.Fatalf("应按时间排序：%+v", evs)
	}
}

func TestRepairSessions(t *testing.T) {
	dir := t.TempDir()
	// 旧版本留下的错乱场次：顺序乱、中间一场没结束
	os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`[
		{"id":1000,"start":1000,"end":0,"title":"a"},
		{"id":5000,"start":5000,"end":6000,"title":"c"},
		{"id":3000,"start":3000,"end":4000,"title":"b"}]`), 0o644)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ss := s.Sessions()
	if len(ss) != 3 || ss[0].Start != 1000 || ss[1].Start != 3000 || ss[2].Start != 5000 || ss[0].End == 0 || ss[0].End > 3000 {
		t.Fatalf("修复后 %+v", ss)
	}
	if cur, _ := s.CurrentSession(); cur.Start != 5000 {
		t.Fatalf("本场应为最后一场：%+v", cur)
	}
}

func TestMigrateLegacy(t *testing.T) {
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "events.jsonl"), []byte("{}\n"), 0o644)
	os.WriteFile(filepath.Join(data, "overlay.json"), []byte("{}"), 0o644)
	room := RoomDir(data, 7)
	MigrateLegacy(data, room)
	if _, err := os.Stat(filepath.Join(room, "events.jsonl")); err != nil {
		t.Fatal("记录应移到直播间目录")
	}
	if _, err := os.Stat(filepath.Join(data, "overlay.json")); err != nil {
		t.Fatal("叠加层设置应留在 data/")
	}
}
