package main

import (
	"crypto/sha256"
	hexenc "encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
)

func repeatingTail(s string, min int) bool {
	if len(s) > 8000 {
		s = s[len(s)-8000:]
	}
	if len(s) < min {
		return false
	}
	for p := 1; 3*p <= len(s); p++ {
		repeats := 4
		if p > 300 {
			repeats = 3
		}
		need := max(min, repeats*p)
		if len(s) < need {
			continue
		}
		covered := p
		for i := len(s) - 1; i >= p; i-- {
			if s[i] != s[i-p] {
				break
			}
			covered++
			if covered >= need {
				return true
			}
		}
	}
	return false
}

type repeatWatch struct{ min, checked int }

func (w *repeatWatch) looping(s string) bool {
	if len(s) < w.min || (w.checked != 0 && len(s)-w.checked < 256) {
		return false
	}
	w.checked = len(s)
	return repeatingTail(s, w.min)
}

func callSig(name, args, output string) string {
	args = strings.TrimSpace(args)
	dec := json.NewDecoder(strings.NewReader(args))
	dec.UseNumber()
	var value, extra any
	if dec.Decode(&value) == nil && dec.Decode(&extra) == io.EOF {
		if canonical, err := json.Marshal(value); err == nil {
			args = string(canonical)
		}
	}
	// Timings and clock times change between identical runs, so they don't count. Other numbers do.
	sum := sha256.Sum256([]byte(timings.ReplaceAllString(strings.TrimSpace(output), "T")))
	return strconv.Itoa(len(name)) + ":" + name + args + "\x00" + hexenc.EncodeToString(sum[:])
}

var timings = regexp.MustCompile(`\b\d+(\.\d+)?\s?(ns|µs|us|ms|s|sec|secs|seconds|m|min|mins|minutes|h)\b|\b\d{1,2}:\d{2}(:\d{2})?(\.\d+)?\b|\b\d{4}-\d{2}-\d{2}(T[0-9:.]+Z?)?\b`)

type callLog struct{ sigs []string }

func (l *callLog) add(sig string) bool {
	l.sigs = append(l.sigs, sig)
	if len(l.sigs) > 9 {
		copy(l.sigs, l.sigs[len(l.sigs)-9:])
		l.sigs = l.sigs[:9]
	}
	for size := 1; size <= 3; size++ {
		start := len(l.sigs) - 3*size
		if start < 0 {
			continue
		}
		same := true
		for i := start + size; i < len(l.sigs); i++ {
			if l.sigs[i] != l.sigs[i-size] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

func (l *callLog) reset() {
	l.sigs = nil
}
