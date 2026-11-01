

// ascii-kit: runtime + helpers for animated ASCII pieces.
// Part of ascii-motion (MIT License, Copyright (c) 2026 ascii-motion contributors).
// mount(), halftone(), hash() and noise() are adapted from ascii.rest by bas3line
// (https://github.com/bas3line/ascii), used under this license:
//
//   MIT License
//
//   Copyright (c) 2026 bas3line (https://github.com/bas3line)
//
//   Permission is hereby granted, free of charge, to any person obtaining a copy
//   of this software and associated documentation files (the "Software"), to deal
//   in the Software without restriction, including without limitation the rights
//   to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
//   copies of the Software, and to permit persons to whom the Software is
//   furnished to do so, subject to the following conditions:
//
//   The above copyright notice and this permission notice shall be included in all
//   copies or substantial portions of the Software.
//
//   THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//   IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//   FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
//   AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
//   LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
//   OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
//   SOFTWARE.
//
// Piece contract (compatible with ascii.rest, MIT):
//   export const meta = { name, note, cols, rows, fps, cell?, palette?, ground?, options? }
//   export default (options) => (t, { paper, color }) => string   // rows joined by "\n"
//   Coloured pieces write a palette index per cell into color[y*cols+x].

// ---------- math ----------
const clamp = (v) => (v < 0 ? 0 : v > 1 ? 1 : v);
const smooth = (a, b, v) => { const k = clamp((v - a) / (b - a)); return k * k * (3 - 2 * k); };
const mix = (a, b, k) => a + (b - a) * k;
const ease = (x) => (x <= 0 ? 0 : x >= 1 ? 1 : x * x * (3 - 2 * x));
function hash(x, y) {
  let h = Math.imul(x | 0, 374761393) + Math.imul(y | 0, 668265263);
  h = Math.imul(h ^ (h >>> 13), 1274126177);
  return ((h ^ (h >>> 16)) >>> 0) / 4294967296;
}
// value noise; wraps every `period` lattice cells in x when period > 0 (for endless drift)
function noise(x, y, period = 0) {
  const xi = Math.floor(x), yi = Math.floor(y), fx = x - xi, fy = y - yi;
  const u = fx * fx * (3 - 2 * fx), v = fy * fy * (3 - 2 * fy);
  let x0 = xi, x1 = xi + 1;
  if (period) { x0 = ((xi % period) + period) % period; x1 = (x0 + 1) % period; }
  const a = hash(x0, yi), b = hash(x1, yi), c = hash(x0, yi + 1), d = hash(x1, yi + 1);
  return a + (b - a) * u + (c - a) * v + (a - b - c + d) * u * v;
}
function fbm(x, y, oct = 4, period = 0) {
  let s = 0, n = 0, amp = 0.5, f = 1;
  for (let i = 0; i < oct; i++) { s += amp * noise(x * f, y * f, period * f); n += amp; amp *= 0.5; f *= 2; }
  return s / n;
}

// ---------- halftone scenes (cell: 1, ~200×100, " ·•●") ----------
const DOTS = " ·•●", COVER = [0, 0.3, 0.6, 1];
const BAYER = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5].map((v) => v / 16 - 0.47);
const hex = (s) => [1, 3, 5].map((i) => parseInt(s.slice(i, i + 2), 16) / 255);
// shade(s, x, y, t, col, row): set s.r s.g s.b (linear-ish 0..1+), optionally s.floor (min dot level), s.fade.
function halftone(meta, shade) {
  const W = meta.cols, H = meta.rows, P = meta.palette.map(hex);
  const lut = new Uint8Array(32768).fill(255);
  const nearest = (r, g, b) => {
    const k = (Math.min(31, (r * 31.99) | 0) << 10) | (Math.min(31, (g * 31.99) | 0) << 5) | Math.min(31, (b * 31.99) | 0);
    if (lut[k] !== 255) return lut[k];
    let best = 0, bd = 1e9;
    for (let i = 0; i < P.length; i++) {
      const dr = P[i][0] - r, dg = P[i][1] - g, db = P[i][2] - b, d = 0.3 * dr * dr + 0.5 * dg * dg + 0.2 * db * db;
      if (d < bd) (bd = d), (best = i);
    }
    return (lut[k] = best);
  };
  const out = new Array(W), lines = new Array(H), s = { r: 0, g: 0, b: 0, floor: 0.3, fade: 1 };
  return (t, { color } = {}) => {
    for (let r = 0; r < H; r++) {
      for (let x = 0; x < W; x++) {
        s.r = s.g = s.b = 0; s.floor = 0.3; s.fade = 1;
        shade(s, x + 0.5, r + 0.5, t, x, r);
        // dot size from brightness, dithered; colour makes up what dot size could not
        const peak = Math.max(s.r, s.g, s.b, 1e-4);
        const level = clamp(s.floor + (1 - s.floor) * Math.pow(peak, 0.85) * 0.95) * s.fade;
        const step = Math.max(0, Math.min(3, Math.round(level * 3 + BAYER[(r & 3) * 4 + (x & 3)])));
        out[x] = DOTS[step];
        if (color) {
          const want = step ? Math.min(1, (level + 0.06) / COVER[step]) : 0, k = (0.3 + 0.7 * want) / peak;
          color[r * W + x] = nearest(clamp(s.r * k), clamp(s.g * k), clamp(s.b * k));
        }
      }
      lines[r] = out.join("");
    }
    return lines.join("\n");
  };
}

// ---------- mono ramp fields (cell: 2, text cells are ~2× taller than wide) ----------
// field(x, y, t) → 0..1 brightness, or -1 for empty. x,y in cell widths from centre (y already ×2).
function ramp(meta, field, RAMP = ".,-~:;=!*#$@") {
  const { cols, rows } = meta, N = RAMP.length, out = new Array(cols), lines = new Array(rows);
  return (t, { paper = false } = {}) => {
    for (let r = 0; r < rows; r++) {
      const y = (r + 0.5 - rows / 2) * 2;
      for (let c = 0; c < cols; c++) {
        const v = field(c + 0.5 - cols / 2, y, t);
        if (v < 0) { out[c] = " "; continue; }
        const i = Math.min(N - 1, Math.floor(clamp(v) * N));
        out[c] = RAMP[paper ? N - 1 - i : i]; // light page: dense ink = dark, so flip
      }
      lines[r] = out.join("");
    }
    return lines.join("\n");
  };
}

// ---------- sprites ----------
const FLIP = { "/": "\\", "\\": "/", "(": ")", ")": "(", "<": ">", ">": "<", "[": "]", "]": "[", "{": "}", "}": "{", "`": "'", "'": "`" };
const mirror = (half, w = Math.max(...half.map((l) => l.length))) =>
  half.map((l) => { l = l.padEnd(w); return l + [...l.slice(0, w - 1)].reverse().map((c) => FLIP[c] || c).join(""); });
function grid(cols, rows) {
  const g = Array.from({ length: rows }, () => new Array(cols).fill(" "));
  return {
    g,
    put(x, y, s, transparent = true) { if (y < 0 || y >= rows) return; for (let i = 0; i < s.length; i++) { const X = x + i; if (X >= 0 && X < cols && !(transparent && s[i] === " ")) g[y][X] = s[i]; } },
    clear() { for (const row of g) row.fill(" "); },
    text() { return g.map((r) => r.join("")).join("\n"); },
  };
}

// ---------- runtime ----------
const rgb = (css) => (css[0] === "#" ? [1, 3, 5].map((i) => parseInt(css.slice(i, i + 2), 16)) : (css.match(/[\d.]+/g) || []).map(Number));
const isDark = (css) => { const c = rgb(css); return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2] < 128; };
function mount(el, piece, options = {}) {
  const meta = piece.meta, { fps = meta.fps ?? 30, motion = false, ...rest } = { ...meta.options, ...options };
  const frame = piece.default(rest);
  const { cols, rows, palette, ground, cell = 2 } = meta;
  const canvas = el instanceof HTMLCanvasElement ? el : null;
  const color = palette && canvas ? new Uint8Array(cols * rows) : undefined;
  let t = 0, draw, ro;
  if (!canvas) {
    draw = () => { el.textContent = frame(t, { paper: isDark(getComputedStyle(el).color) }); };
  } else {
    const ctx = canvas.getContext("2d");
    let w = 0, h = 0;
    canvas.style.display ||= "block"; canvas.style.width ||= "100%";
    canvas.style.aspectRatio = `${cols} / ${rows * cell}`;
    const size = () => {
      w = (canvas.clientWidth * (devicePixelRatio || 1)) / cols; h = w * cell;
      canvas.width = Math.round(w * cols); canvas.height = Math.round(h * rows);
    };
    const buckets = new Map();
    draw = () => {
      const ink = getComputedStyle(canvas).color;
      const text = frame(t, { paper: ground ? !isDark(ground) : isDark(ink), color });
      if (ground) { ctx.fillStyle = ground; ctx.fillRect(0, 0, canvas.width, canvas.height); } else ctx.clearRect(0, 0, canvas.width, canvas.height);
      ctx.font = `${w / 0.6}px ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`;
      ctx.textAlign = "center"; ctx.textBaseline = "middle";
      buckets.forEach((a) => (a.length = 0));
      for (let k = 0, x = 0, y = 0; k < text.length; k++) {
        const c = text.charCodeAt(k);
        if (c === 10) { x = 0; y++; continue; }
        if (c !== 32) { const ci = color ? color[y * cols + x] : 0; let a = buckets.get(ci); if (!a) buckets.set(ci, (a = [])); a.push(c, x, y); }
        x++;
      }
      buckets.forEach((a, ci) => {
        if (!a.length) return;
        ctx.fillStyle = palette ? palette[ci] : ink; // one fillStyle per colour
        for (let i = 0; i < a.length; i += 3) ctx.fillText(String.fromCharCode(a[i]), (a[i + 1] + 0.5) * w, (a[i + 2] + 0.5) * h);
      });
    };
    size();
    ro = new ResizeObserver(() => { size(); draw(); }); ro.observe(canvas);
  }
  draw();
  if (!fps) return () => ro?.disconnect();
  const still = matchMedia("(prefers-reduced-motion: reduce)");
  let raf = 0, last = 0, seen = false;
  const tick = (now) => { raf = requestAnimationFrame(tick); const dt = now - last; if (dt < 1000 / fps - 2) return; last = now; t += Math.min(dt, 100) / 1000; draw(); };
  const run = () => {
    const go = seen && !document.hidden && (motion || !still.matches);
    if (go && !raf) { last = performance.now(); raf = requestAnimationFrame(tick); } else if (!go && raf) { cancelAnimationFrame(raf); raf = 0; }
  };
  const io = new IntersectionObserver((e) => { seen = e[e.length - 1].isIntersecting; run(); }); io.observe(el);
  document.addEventListener("visibilitychange", run); still.addEventListener("change", run);
  return () => { io.disconnect(); ro?.disconnect(); cancelAnimationFrame(raf); raf = 0; document.removeEventListener("visibilitychange", run); still.removeEventListener("change", run); };
}


const meta={name:'地球 · 启航',note:'地球海岸发射场逐步显现，火箭点火轻震后加速升空，烟雾沿地面扩散。',cols:120,rows:34,cell:2,fps:20,ground:'#080e24',palette:['#172b50','#385477','#6f98b8','#b6d9df','#e9e7d7','#b9534c','#f19872','#a775aa','#d4ae83','#78644f','#fff2bd','#75a6be']};
function make(){return(t,{color})=>{let out=[];const flight=Math.max(0,t-7),p=Math.min(1,flight/13),j=t>4&&t<10?Math.sin((t-4)/6*Math.PI):0,cy=78-230*p*p+j*.45*Math.sin(t*24),cx=122+j*.6*Math.sin(t*21);for(let r=0;r<34;r++){let line='';for(let c=0;c<120;c++){let bits=0,col=0;for(let dy=0;dy<4;dy++)for(let dx=0;dx<2;dx++){const x=c*2+dx,y=(r*4+dy)*1.2;let on=false,k=0;
if(hash(c,r)> .969&&y<112){const twinkle=hash(c+91,r+37)>.92;on=true;k=twinkle?(Math.sin(t*1.1+hash(c,r)*20)>.55?3:1):2;}
for(const [px,py,rad,cl] of [[198,26,12,4]]){let xx=x-px-Math.sin(t*.18+px)*1.1,yy=(y-py)/1.2;let d=Math.hypot(xx,yy);if(d<rad){on=hash(x,y)>.24;k=cl;if(Math.hypot(xx+rad*.35,yy+rad*.3)<rad*.25)k=0;}if(px===35&&Math.abs(yy*.8+xx*.35)<1.8&&Math.abs(xx)<40){on=true;k=8;}}
for(let m=0;m<2;m++){const clock=(t+m*8)%19,age=clock-5;if(age>=0&&age<1.35){const headX=65+m*82+age*40,headY=8+age*15;const trail=(headX-x)/40;const distance=Math.abs(y-(headY-trail*15));if(trail>=0&&trail<.48&&distance<.7&&y<115){on=true;k=trail<.07?4:trail<.22?3:1;}}}
let horizon=139+2*Math.sin(x*.035);if(y>horizon){on=hash(x,y)>.63;k=0;if(x<75||x>169){on=Math.sin(y*1.7+x*.08+t*.65)>.87;k=2;}else{k=9;}}
// Low coastal hills and the launch apron.
if(y>132+5*Math.sin(x*.036)&&y<140){on=true;k=0;}
if(y>=140&&y<144&&x>95&&x<150){on=true;k=3;}
// Service tower with open lattice, below the rocket's nose.
if(x>88&&x<96&&y>86&&y<141){on=Math.abs(x-89)<1||Math.abs(x-95)<1||Math.abs(((y-86)%12)-(x-88)*1.6)<1;k=2;}
if(y>96&&y<98&&x>=95&&x<111){on=true;k=3;}
let X=x-cx,Y=y-cy;let width=Y<15?Math.sqrt(Math.max(0,1-((Y-15)/15)**2))*11:11-(Y-15)*.035;if(Y>=0&&Y<51&&Math.abs(X)<width){on=true;k=(Math.floor(Y/13)+Math.floor((X+12)/11))%2?6:4;if(Y<12)k=6;let d=Math.hypot(X,Y-24);if(d<7){k=d>5?6:d>4?4:0;}}
if(Y>40&&Y<62&&Math.abs(X)>8&&Math.abs(X)<19-(Y-40)*.25){on=true;k=5;}if(Y>50&&Y<57&&Math.abs(X)<7){on=true;k=9;}
if(t>4&&Y>57&&Y<57+Math.min(36,(t-4)*5)&&Math.abs(X)<(7-(Y-57)*.13)*(.8+.2*Math.sin(t*22+y))){on=true;k=Y<65?10:11;}
if(t>4&&t<25&&y>127&&y<148&&Math.abs(x-122)<Math.min(90,(t-4)*9)){const fade=Math.max(0,1-Math.max(0,t-14)/11),edge=1-Math.abs(y-139)/13;let d=Math.sin(x*.14-t*1.4)+Math.sin(y*.5+t*.7);if(d> .5&&hash(x,y)>.68-fade*.23&&edge>0){on=true;k=t<8?3:2;}}
if(on && (t>=3 || hash(c,r+731)<Math.max(0,(t-.2)/2.8))){bits|=1<<[[0,3],[1,4],[2,5],[6,7]][dy][dx];col=k;}}
line+=String.fromCharCode(0x2800+bits);color[r*120+c]=col;}out.push(line);}return out.join('\n');};}


const f=make();for(const t of [0,3,5,8,12,20,26]){const rows=f(t,{color:new Uint8Array(4080)}).split('\n');if(rows.length!==34||rows.some(r=>r.length!==120))throw Error('Frame dimensions');}console.log('Launch frames validated');