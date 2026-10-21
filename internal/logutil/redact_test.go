package logutil

import "testing"

func TestRedactorMasksLongestFirst(t *testing.T) {
	r := NewRedactor([]string{"hunter2", "hunter2-extended", "ok", ""})
	got := r.Apply("token=hunter2-extended other=hunter2 short=ok")
	want := "token=[redacted] other=[redacted] short=ok"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRedactorNilIsNoop(t *testing.T) {
	var r *Redactor
	if r.Apply("x") != "x" {
		t.Fatal("nil redactor must pass through")
	}
	if NewRedactor([]string{"abc"}) != nil {
		t.Fatal("short values produce no redactor")
	}
}
