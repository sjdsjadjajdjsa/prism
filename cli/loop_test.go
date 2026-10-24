package main

import (
	"crypto/sha256"
	"encoding/base64"
	hexenc "encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func loopTestBytes(n int) string {
	r := rand.New(rand.NewSource(42))
	b := make([]byte, n)
	_, _ = r.Read(b)
	return string(b)
}

func loopTestProse() string {
	var b strings.Builder
	for i := 0; b.Len() < 8000; i++ {
		fmt.Fprintf(&b, "Section %d describes a different observation from the journey. We compared the available evidence, considered alternatives, and recorded finding %d before moving onward.\n", i, i*i)
	}
	return b.String()[:8000]
}

func TestRepeatingTail(t *testing.T) {
	prose := loopTestProse()
	paragraph := prose[:400]
	loops := map[string]string{
		"sentence": "We should review the evidence before deciding what happens next. ",
		"line":     "A repeated line of output.\n",
	}
	for name, piece := range loops {
		t.Run(name, func(t *testing.T) {
			if !repeatingTail(strings.Repeat(piece, 60), 1200) {
				t.Fatal("missed repeated text")
			}
		})
	}
	for name, s := range map[string]string{
		"paragraph":     strings.Repeat(paragraph, 4),
		"short period":  strings.Repeat("ab", 1000),
		"partial block": strings.Repeat(paragraph, 4) + paragraph[:173],
		"normal prefix": prose[:3000] + strings.Repeat(paragraph, 4) + paragraph[:173],
		"binary":        strings.Repeat("\x00\xff\x80", 500),
		"large stream":  prose + strings.Repeat("ab", 5000),
	} {
		t.Run(name, func(t *testing.T) {
			if !repeatingTail(s, 1200) {
				t.Fatal("missed repeating tail")
			}
		})
	}

	var numbered, table, code strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&numbered, "%d. apple and banana harvest from orchard %d\n", i, i)
	}
	table.WriteString("| Name | Description |\n|---|---|\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&table, "| item %d | A distinct measurement of %d units in region %d |\n", i, i*i, i+3)
	}
	code.WriteString("package example\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&code, "func task%d() error {\nerr := run(%d)\nif err != nil { return err }\nreturn nil\n}\n", i, i)
	}
	objects := make([]map[string]any, 50)
	for i := range objects {
		objects[i] = map[string]any{"id": i, "name": fmt.Sprintf("unique object %d", i)}
	}
	array, err := json.MarshalIndent(objects, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]string{
		"prose":        prose,
		"markdown":     "# Observations\n\n" + prose[:4000] + "\n## Conclusions\n\n" + prose[4000:],
		"numbered":     numbered.String(),
		"table":        table.String(),
		"go source":    code.String(),
		"divider":      prose + "\n" + strings.Repeat("=", 80),
		"base64":       base64.StdEncoding.EncodeToString([]byte(loopTestBytes(6000))),
		"json array":   string(array),
		"loop not end": strings.Repeat("ab", 5000) + prose,
	} {
		t.Run(name, func(t *testing.T) {
			if repeatingTail(s, 1200) {
				t.Fatal("ordinary text detected as looping")
			}
		})
	}
}

func TestRepeatingTailBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name string
		s    string
		min  int
		want bool
	}{
		{"empty", "", 0, false},
		{"negative min", "", -1, false},
		{"one byte", "a", 0, false},
		{"three copies", "aaa", 0, false},
		{"four copies", "aaaa", 0, true},
		{"partial fewer than four", "abcabcabcab", 10, false},
		{"partial four copies", "abcabcabcabcab", 14, true},
		{"min exact", strings.Repeat("ab", 600), 1200, true},
		{"min not met", strings.Repeat("ab", 600), 1201, false},
		{"period 300 three", strings.Repeat(loopTestBytes(300), 3), 900, false},
		{"period 300 four", strings.Repeat(loopTestBytes(300), 4), 1200, true},
		{"period 301 three", strings.Repeat(loopTestBytes(301), 3), 903, true},
		{"period 1500", strings.Repeat(loopTestBytes(1500), 3), 4500, true},
		{"period 1501", strings.Repeat(loopTestBytes(1501), 4), 4503, true},
		{"period 2666 three", strings.Repeat(loopTestBytes(2666), 3), 7998, true},
		{"period 2700 too long for the window", strings.Repeat(loopTestBytes(2700), 3), 7000, false},
		{"window limit", strings.Repeat("a", 9000), 8001, false},
		{"window exact", strings.Repeat("a", 9000), 8000, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := repeatingTail(tt.s, tt.min); got != tt.want {
				t.Fatalf("repeatingTail(..., %d) = %v, want %v", tt.min, got, tt.want)
			}
		})
	}
}

func TestRepeatWatch(t *testing.T) {
	w := repeatWatch{min: 1200}
	for _, tt := range []struct {
		n       int
		want    bool
		checked int
	}{
		{1199, false, 0},
		{1200, true, 1200},
		{1200, false, 1200},
		{1455, false, 1200},
		{1456, true, 1456},
		{1711, false, 1456},
		{1712, true, 1712},
	} {
		if got := w.looping(strings.Repeat("a", tt.n)); got != tt.want || w.checked != tt.checked {
			t.Fatalf("length %d: got (%v, %d), want (%v, %d)", tt.n, got, w.checked, tt.want, tt.checked)
		}
	}
	w = repeatWatch{min: 3}
	if w.looping("abc") || w.checked != 3 {
		t.Fatal("first check must run even before 256 bytes")
	}
	if w.looping(strings.Repeat("a", 258)) || !w.looping(strings.Repeat("a", 259)) {
		t.Fatal("checks must be spaced by 256 bytes even after a non-loop")
	}
}

func TestRepeatingTailProseSpeed(t *testing.T) {
	s := loopTestProse()
	start := time.Now()
	for i := 0; i < 2000; i++ {
		if repeatingTail(s, 1200) {
			t.Fatal("ordinary prose detected as looping")
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("2000 checks of 8 KB prose took %s", elapsed)
	}
}

func BenchmarkRepeatingTailProse(b *testing.B) {
	s := loopTestProse()
	b.SetBytes(int64(len(s)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		repeatingTail(s, 1200)
	}
}

func TestCallSig(t *testing.T) {
	a := callSig("read", ` { "b": [2, {"y": 3, "x": 4}], "a": 1 } `, " result\n")
	b := callSig("read", `{"a":1,"b":[2,{"x":4,"y":3}]}`, "result")
	if a != b {
		t.Fatal("JSON key order, spacing, or output whitespace changed signature")
	}
	sum := sha256.Sum256([]byte("result"))
	if want := "4:read" + `{"a":1,"b":[2,{"x":4,"y":3}]}` + "\x00" + hexenc.EncodeToString(sum[:]); a != want {
		t.Fatal("signature must include canonical args and sha256 output")
	}
	if a == callSig("read", `{"a":1,"b":[2,{"x":4,"y":3}]}`, "different") {
		t.Fatal("different output must change signature")
	}
	if a == callSig("write", `{"a":1,"b":[2,{"x":4,"y":3}]}`, "result") {
		t.Fatal("different tool must change signature")
	}
	if callSig("tool", " raw args \n", "ok") != callSig("tool", "raw args", "ok") {
		t.Fatal("non-JSON args must be trimmed")
	}
	if callSig("tool", "{} trailing", "ok") == callSig("tool", "{}", "ok") {
		t.Fatal("trailing non-JSON content must be preserved")
	}
	if callSig("tool", `{"id":9007199254740992}`, "") == callSig("tool", `{"id":9007199254740993}`, "") {
		t.Fatal("large integer arguments must stay distinct")
	}
	if callSig("tool", "a\x00b", "c") == callSig("tool\x00a", "b", "c") {
		t.Fatal("tool and argument boundaries must stay distinct")
	}
}

func TestCallLog(t *testing.T) {
	for _, tt := range []struct {
		calls string
		want  bool
	}{
		{"AAA", true},
		{"AABA", false},
		{"ABABAB", true},
		{"ABCABCABC", true},
		{"ABABA", false},
		{"XYZABABAB", true},
		{"XYZABCABCABC", true},
		{"ABCDEFGHIJKL", false},
	} {
		t.Run(tt.calls, func(t *testing.T) {
			var l callLog
			var got bool
			for _, c := range tt.calls {
				got = l.add(string(c))
				if len(l.sigs) > 9 {
					t.Fatal("retained more than nine signatures")
				}
			}
			if got != tt.want {
				t.Fatalf("add = %v, want %v", got, tt.want)
			}
		})
	}
	var l callLog
	l.add("A")
	l.add("A")
	l.reset()
	if len(l.sigs) != 0 || l.add("A") || l.add("A") || !l.add("A") {
		t.Fatal("reset must require three fresh repeats")
	}
}
