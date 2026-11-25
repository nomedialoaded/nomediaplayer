"""Drive nomediaplayer headless in a pseudo-terminal (for tests and screenshots).

usage: uv run -q --with pyte python tools/ttydrive.py OUTPREFIX step...
  k:<keys>   type keys (<enter> <esc> <tab> <up> <down> <left> <right> <space>)
  w:<sec>    wait
  r:         force a full redraw (resize)
  s:<name>   save the screen as text  -> OUTPREFIX<name>.txt
  h:<name>   save the screen as HTML  -> OUTPREFIX<name>.html
env: NMP=binary to run (default nomediaplayer)
"""
import fcntl, html, os, pty, re, select, signal, struct, sys, termios, time
import pyte

COLS, ROWS = 190, 52
out, steps = sys.argv[1], sys.argv[2:]
screen = pyte.Screen(COLS, ROWS)
stream = pyte.ByteStream(screen)
pid, fd = pty.fork()
if pid == 0:
    os.environ.update(TERM="xterm-256color", COLORTERM="truecolor")
    os.execvp(os.environ.get("NMP", "nomediaplayer"), [os.environ.get("NMP", "nomediaplayer")])

def size(c):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, c, 0, 0))
    os.kill(pid, signal.SIGWINCH)

def pump(sec):
    end = time.time() + sec
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.05)
        if r:
            try:
                data = os.read(fd, 65536)
            except OSError:
                return
            if b"\x1b[6n" in data:
                os.write(fd, b"\x1b[1;1R")
            if b"\x1b]11;?" in data:
                os.write(fd, b"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\")
            stream.feed(re.sub(rb"\x1b_G.*?\x1b\\\\", b"", data, flags=re.S))

KEYS = {"<enter>": "\r", "<esc>": "\x1b", "<tab>": "\t", "<up>": "\x1b[A", "<down>": "\x1b[B",
        "<right>": "\x1b[C", "<left>": "\x1b[D", "<space>": " "}

def send(k):
    i = 0
    while i < len(k):
        for name, seq in KEYS.items():
            if k.startswith(name, i):
                os.write(fd, seq.encode()); i += len(name); break
        else:
            os.write(fd, k[i].encode()); i += 1
        pump(0.15)

BASIC = {"black": "#1e1e2e", "red": "#f38ba8", "green": "#a6e3a1", "brown": "#f9e2af", "blue": "#89b4fa",
         "magenta": "#cba6f7", "cyan": "#94e2d5", "white": "#cdd6f4"}

def col(c, default):
    if c == "default": return default
    if c in BASIC: return BASIC[c]
    if c.startswith("bright"): return BASIC.get(c[6:], default)
    if len(c) == 6: return "#" + c
    return default

def snap_html(name):
    FG, BG = "#cdd6f4", "#1e1e2e"
    rows = []
    for y in range(ROWS):
        line, parts, cur, buf = screen.buffer[y], [], None, ""
        for x in range(COLS):
            ch = line[x]
            fg, bg = col(ch.fg, FG), col(ch.bg, BG)
            if ch.reverse: fg, bg = bg, fg
            st = (fg, bg, ch.bold, ch.italics, ch.underscore)
            if st != cur:
                if cur: parts.append((cur, buf))
                cur, buf = st, ""
            buf += ch.data or " "
        parts.append((cur, buf))
        rows.append("".join(
            f'<span style="color:{f};background:{b};{"font-weight:700;" if bo else ""}'
            f'{"font-style:italic;" if it else ""}{"text-decoration:underline;" if un else ""}">{html.escape(t)}</span>'
            for (f, b, bo, it, un), t in parts))
    doc = (f'<!doctype html><meta charset=utf-8><style>body{{margin:0;background:{BG}}}pre{{margin:0;padding:14px;'
           f'font:15px/1.22 "JetBrainsMono Nerd Font","JetBrains Mono","DejaVu Sans Mono",monospace;background:{BG}}}'
           f'span{{white-space:pre}}</style><pre>{chr(10).join(rows)}</pre>')
    open(f"{out}{name}.html", "w").write(doc)

size(COLS)
pump(4)
for s in steps:
    kind, arg = s.split(":", 1)
    if kind == "k": send(arg)
    elif kind == "w": pump(float(arg))
    elif kind == "s": open(f"{out}{arg}.txt", "w").write("\n".join(screen.display))
    elif kind == "h": snap_html(arg)
    elif kind == "r":
        size(COLS - 1); pump(0.6); screen.reset(); size(COLS); pump(1.2)
os.kill(pid, signal.SIGTERM); time.sleep(0.3)
try: os.kill(pid, signal.SIGKILL)
except ProcessLookupError: pass
