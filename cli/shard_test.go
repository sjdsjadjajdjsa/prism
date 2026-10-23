package main

import "testing"

func TestShardMouthCentered(t *testing.T) {
	cases := []struct {
		name string
		md   mood
		want map[int]rune
	}{
		{"idle", moodIdle, map[int]rune{11: '▄', 14: '▄'}},
		{"thinking", moodThinking, map[int]rune{12: '▄', 13: '▄'}},
		{"working", moodWorking, map[int]rune{11: '▄', 14: '▄'}},
		{"happy", moodHappy, map[int]rune{11: '▀', 12: '▄', 13: '▄', 14: '▀'}},
		{"sleepy", moodSleepy, map[int]rune{12: '▄', 13: '▄'}},
		{"surprised", moodSurprised, map[int]rune{12: '█', 13: '█'}},
		{"proud", moodProud, map[int]rune{11: '▀', 12: '▄', 13: '▄', 14: '▀'}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const x, y = 5, 2
			th := themes[0]
			cv := newCanvas(50, 20, th.bg, th.fg)
			drawShard(cv, th, x, y, tc.md, 1800)
			dark := mix(th.bg, rgb{0, 0, 0}, 0.3)
			found := make(map[int]rune)
			for i := 0; i < 26; i++ {
				c := cv.get(x+i, y+shardMouthRow)
				if c.fg != dark {
					continue
				}
				found[i] = c.ch
				if !shardPixels(th, tc.md, 1800, 0)[shardMouthRow][i/2].on {
					t.Errorf("mouth cell %d outside body", i)
				}
			}
			if len(found) != len(tc.want) {
				t.Errorf("mouth cells = %v, want %v", found, tc.want)
			}
			for i, glyph := range found {
				if tc.want[i] != glyph {
					t.Errorf("cell %d = %q, want %q", i, glyph, tc.want[i])
				}
				if found[25-i] != glyph {
					t.Errorf("cell %d (%q) not symmetric with cell %d (%q)", i, glyph, 25-i, found[25-i])
				}
			}
			if tc.md == moodIdle || tc.md == moodWorking {
				for _, i := range []int{12, 13} {
					if c := cv.get(x+i, y+shardMouthRow); c.ch != '█' || c.fg == dark {
						t.Errorf("space at cell %d painted over body: %+v", i, *c)
					}
				}
			}
			if tc.md == moodIdle {
				for _, i := range []int{8, 9, 16, 17} {
					if c := cv.get(x+i, y+shardEyeRow); c.ch != '█' || c.fg != dark {
						t.Errorf("eye cell %d not symmetric: %+v", i, *c)
					}
				}
			}
		})
	}
}
