(function(global) {
  'use strict';

  function parseHex(s) {
    if (typeof s !== 'string') return { r: 0, g: 0, b: 0 };
    var clean = s.replace(/^#/, '');
    if (clean.length === 3) {
      clean = clean[0] + clean[0] + clean[1] + clean[1] + clean[2] + clean[2];
    }
    var num = parseInt(clean, 16);
    if (isNaN(num)) return { r: 0, g: 0, b: 0 };
    return {
      r: (num >> 16) & 255,
      g: (num >> 8) & 255,
      b: num & 255
    };
  }

  function toRgbStr(c) {
    return 'rgb(' + Math.round(c.r) + ',' + Math.round(c.g) + ',' + Math.round(c.b) + ')';
  }

  function mix(a, b, t) {
    t = Math.max(0, Math.min(1, t));
    var c1 = typeof a === 'string' ? parseHex(a) : a;
    var c2 = typeof b === 'string' ? parseHex(b) : b;
    return {
      r: Math.round(c1.r + (c2.r - c1.r) * t),
      g: Math.round(c1.g + (c2.g - c1.g) * t),
      b: Math.round(c1.b + (c2.b - c1.b) * t)
    };
  }

  function at(theme, v) {
    if (global.PrismShard && typeof global.PrismShard.at === 'function') {
      return global.PrismShard.at(theme, v);
    }
    var spec = theme && theme.spectrum ? theme.spectrum : ['#ff5f6d', '#6fd3ff', '#c69bff'];
    if (!spec.length) return { r: 255, g: 255, b: 255 };
    v = v - Math.floor(v);
    var n = spec.length;
    var p = v * n;
    var i = Math.floor(p) % n;
    var frac = p - Math.floor(p);
    var c1 = typeof spec[i] === 'string' ? parseHex(spec[i]) : spec[i];
    var c2 = typeof spec[(i + 1) % n] === 'string' ? parseHex(spec[(i + 1) % n]) : spec[(i + 1) % n];
    return mix(c1, c2, frac);
  }

  function ease(v) {
    v = Math.max(0, Math.min(1, v));
    return 1 - Math.pow(1 - v, 3);
  }

  function hash(a, b, c) {
    var x = ((a * 374761393 + b * 668265263 + c * 1442695041) >>> 0);
    x = (Math.imul(x ^ (x >>> 13), 1274126177) >>> 0);
    x ^= (x >>> 16);
    return (x % 10000) / 10000;
  }

  function beamShake(n) {
    if (n < 820 || n > 1060) {
      return 0;
    }
    var cycle = [1, -1, 1, 0];
    return cycle[Math.floor((n - 820) / 60) % 4];
  }

  var activeAnimation = null;

  function render(ctx, w, h, originX, originY, ms, theme, cell) {
    var n = ms;
    var charge = ease(n / 350);
    var converge = ease((n - 350) / 450);
    var fire = ease((n - 800) / 250);
    var fade = ease((n - 1250) / 500);

    if (n < 0 || fade >= 1) {
      return;
    }

    var cw = (cell && cell.w) || (typeof cell === 'number' ? cell : 10);
    var ch = (cell && cell.h) || (typeof cell === 'number' ? cell * 2 : 20);

    var cols = Math.ceil(w / cw);
    var rows = Math.ceil(h / ch);

    var sx = originX !== undefined ? originX : cols - 1;
    var sy = originY !== undefined ? originY : Math.floor(rows / 2);

    var tBg = theme && theme.bg ? (typeof theme.bg === 'string' ? parseHex(theme.bg) : theme.bg) : { r: 11, g: 11, b: 16 };
    var tFg = theme && theme.fg ? (typeof theme.fg === 'string' ? parseHex(theme.fg) : theme.fg) : { r: 232, g: 230, b: 240 };
    var white = { r: 255, g: 255, b: 255 };

    // Screen dim before 1250ms — darkens the whole frame evenly toward mix(bg, black, 0.6),
    // mirroring the per-cell fg/bg dim in drawBeam (cli/shard.go).
    if (n < 1250) {
      var dim = Math.max(0, Math.min(1, 0.35 * charge * (1 - fire * 0.4)));
      var dimBg = mix(tBg, { r: 0, g: 0, b: 0 }, 0.6);
      ctx.fillStyle = 'rgba(' + dimBg.r + ',' + dimBg.g + ',' + dimBg.b + ',' + dim + ')';
      ctx.fillRect(0, 0, w, h);
    }

    // White flash peaking at 820ms over 90ms
    var flash = 1 - Math.abs(n - 820) / 90;
    if (flash > 0) {
      var flashMix = mix(tBg, white, 0.35);
      var flashAlpha = flash * 0.5;
      ctx.fillStyle = 'rgba(' + flashMix.r + ',' + flashMix.g + ',' + flashMix.b + ',' + flashAlpha + ')';
      ctx.fillRect(0, 0, w, h);
    }

    var length = sx; // leftward length
    var dimNow = 0.0;
    if (n < 1250) {
      dimNow = 0.35 * charge * (1 - fire * 0.4);
    }
    var shade = mix(tBg, mix(tBg, { r: 0, g: 0, b: 0 }, 0.6), dimNow);
    if (flash > 0) {
      shade = mix(shade, mix(tBg, white, 0.35), flash * 0.5);
    }

    var rays = 7;

    // cells already painted by rays / glow / beam — sparkles only land on empty cells
    // (terminal check: c.ch == ' ')
    var drawn = Object.create(null);
    function markCell(mx, my) { drawn[mx + ',' + my] = 1; }
    function cellTaken(mx, my) { return drawn[mx + ',' + my] === 1; }
    var shadeHalf = 'rgba(' + shade.r + ',' + shade.g + ',' + shade.b + ',0.5)';

    // Phase 1: Rays and glow point (drawn ONLY while fire == 0)
    if (fire === 0) {
      var spread = (1 - converge) * 0.5;
      for (var i = 0; i < rays; i++) {
        var off = (i / (rays - 1) - 0.5) * 2;
        var angle = off * spread + Math.sin(n / 120 + i) * 0.02 * (1 - converge);
        var rayCol = mix(at(theme, i / rays), tFg, 0.2 + 0.5 * converge);
        var rayDist = Math.floor(length * charge);

        for (var d = 0; d < rayDist; d++) {
            var rx = sx - d;
          if (rx < 0 || rx > sx) continue;
          var ry = sy + Math.round(Math.tan(angle) * d * 0.5);
          if (ry < 0 || ry >= rows) continue;
          // terminal: c.bg = mix(c.bg, shade, 0.5) then cv.set(x, y, '━', col, true)
          ctx.fillStyle = shadeHalf;
          ctx.fillRect(rx * cw, ry * ch, cw, ch);
          var barH = Math.max(1, Math.round(ch * 0.2));
          var barY = ry * ch + Math.round((ch - barH) / 2);
            ctx.fillStyle = toRgbStr(rayCol);
          ctx.fillRect(rx * cw, barY, cw, barH);
          markCell(rx, ry);
        }
      }

      if (converge > 0) {
        var glowCol = mix(at(theme, n / 800), white, 0.6);
        for (var r = 0; r < 3; r++) {
          var gx = sx - r;
          if (gx < 0 || gx > sx) continue;
          // terminal: c.bg = shade, then '█' in mix(shade, glowCol, …) on top
          ctx.fillStyle = toRgbStr(shade);
          ctx.fillRect(gx * cw, sy * ch, cw, ch);
            var cellCol = mix(shade, glowCol, converge * (1 - r * 0.3));
          ctx.fillStyle = toRgbStr(cellCol);
          ctx.fillRect(gx * cw, sy * ch, cw, ch);
          markCell(gx, sy);
        }
      }
    }

    // Phase 2: Firing beam (once firing, solid beam leftward from originX to left edge)
    if (fire > 0) {
      var beamCols = Math.floor(length * fire);
      var half = 1 + Math.floor(fire * 1.5 * (1 - fade));

      for (var bd = 0; bd < beamCols; bd++) {
        var bx = sx - bd;
        if (bx < 0 || bx > sx) continue;
        var pulse = 0.5 + 0.5 * Math.sin(bd / 4 - n / 45);

        for (var dy = -half; dy <= half; dy++) {
          var by = sy + dy;
          if (by < 0 || by >= rows) continue;

          var core = 1 - Math.abs(dy) / (half + 1);
          var col = mix(at(theme, bd / 18 - n / 300 + dy * 0.05), white, core * (0.5 + 0.3 * pulse));
          var finalCol = mix(col, tBg, fade);
          ctx.fillStyle = toRgbStr(finalCol);

          // Straight crisp top/bottom edges:
          // dy == -half: '▄' (bottom half of cell)
          // dy == half:  '▀' (top half of cell)
          // dy in between: '█' (full cell)
          var cellX = bx * cw;
          var cellY = by * ch;
          if (dy === -half) {
            var topH = Math.floor(ch / 2);
            ctx.fillRect(cellX, cellY + (ch - topH), cw, topH);
          } else if (dy === half) {
            var botH = Math.floor(ch / 2);
            ctx.fillRect(cellX, cellY, cw, botH);
          } else {
            ctx.fillRect(cellX, cellY, cw, ch);
          }
          markCell(bx, by);
        }
      }

      // Sparkles: drift outward and fade, never drawn right of the origin
      var age = (n - 800) / 1000;
      var sparkleGlyphs = ['✦', '·', '✧', '•'];
      var fontSize = Math.max(9, Math.floor(ch * 0.8));
      ctx.font = fontSize + 'px monospace';
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';

      for (var si = 0; si < 28; si++) {
        var sd = hash(si, 1, 2) * length * fire;
        var drift = age * (2 + hash(si, 8, 3) * 5);
        var sign = hash(si, 4, 1) - 0.5 < 0 ? -1 : 1;
        var spy = sy + Math.round((hash(si, 3, 7) - 0.5) * 4 + Math.abs(drift) * sign);
        // dir is -1 (leftward): px = sx - int(d + age*hash*6*dir) -> in Go: sx + dir*int(d + age*hash*6*float64(dir))
        // Since dir is -1, float64(dir) is -1.0, so d + age*hash*6*(-1) -> px = sx - int(d - age*hash*6)
        var spx = sx - Math.floor(sd - age * hash(si, 6, 2) * 6);
        var life = 1 - Math.max(0, age - hash(si, 9, 9) * 0.6) / 0.9;

        if (life <= 0) continue;
        if (spx > sx) continue; // never drawn right of the origin
        if (spx < 0 || spy < 0 || spy >= rows) continue;

        // terminal: only lands where c.ch == ' ' — i.e. no ray, glow or beam cell here
        if (cellTaken(spx, spy)) continue;

        var spCol = mix(tBg, at(theme, hash(si, 5, 6)), life);
        ctx.fillStyle = toRgbStr(spCol);
        ctx.fillText(sparkleGlyphs[si % 4], spx * cw + cw / 2, spy * ch + ch / 2);
      }
    }
  }

  function play(opts) {
    if (!opts) return function() {};

    // Refuse to start if already running
    if (activeAnimation) {
      return function() {};
    }

    // Prefers-reduced-motion check
    if (typeof window !== 'undefined' && window.matchMedia) {
      var mq = window.matchMedia('(prefers-reduced-motion: reduce)');
      if (mq && mq.matches) {
        return function() {};
      }
    }

    var canvas = opts.canvas;
    if (!canvas) return function() {};

    var ctx = canvas.getContext('2d');
    var theme = opts.theme || {};
    var onShake = typeof opts.onShake === 'function' ? opts.onShake : null;
    var cell = opts.cell || { w: 10, h: 20 };

    var startTime = null;
    var rafId = null;
    var stopped = false;

    function stop() {
      if (stopped) return;
      stopped = true;
      if (rafId) {
        cancelAnimationFrame(rafId);
        rafId = null;
      }
      if (activeAnimation === stop) {
        activeAnimation = null;
      }
      if (onShake) {
        onShake(0);
      }
    }

    activeAnimation = stop;

    function frame(now) {
      if (stopped) return;
      if (startTime === null) startTime = now;
      var elapsed = now - startTime;

      if (elapsed >= 1750) {
        stop();
        return;
      }

      var w = canvas.width;
      var h = canvas.height;
      var cw = cell.w || 10;
      var ch = cell.h || 20;
      var cols = Math.ceil(w / cw);
      var rows = Math.ceil(h / ch);

      var ox = opts.originX !== undefined ? opts.originX : cols - 1;
      var oy = opts.originY !== undefined ? opts.originY : Math.floor(rows / 2);

      if (onShake) {
        var dx = beamShake(elapsed);
        onShake(dx);
      }

      ctx.clearRect(0, 0, w, h);
      render(ctx, w, h, ox, oy, elapsed, theme, cell);

      rafId = requestAnimationFrame(frame);
    }

    rafId = requestAnimationFrame(frame);
    return stop;
  }

  var PrismBeam = {
    render: render,
    play: play,
    mix: mix,
    at: at,
    beamShake: beamShake,
    ease: ease
  };

  global.PrismBeam = PrismBeam;
  if (typeof module !== 'undefined' && module.exports) {
    module.exports = PrismBeam;
  }
})(typeof window !== 'undefined' ? window : globalThis);
