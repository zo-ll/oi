# tide

Minimal async terminal toolkit for Go fullscreen TUI apps.

Tide is the layer *below* a TUI framework: frame painting at absolute row/col,
channel-based input you select on, escape/mouse parsing, modal widgets, and a
scroll viewport. No reactive model, no component tree, no app scaffold — you
build your own event loop and call tide to paint and read. Standard library
only.

It exists for apps that want a fullscreen terminal UI without adopting a full
framework. Application state, transcripts, tool dispatch, and domain rendering
belong in the app; tide stays the small terminal surface underneath.

## What it provides

- **Terminal** — raw mode, alternate screen, SGR mouse enable/disable, cached
  size refreshed on `SIGWINCH` via `WatchResize`, cursor + style helpers
  (`HideCursor`/`ShowCursor`/`MoveTo`/`ClearLine`/`Dim`/`Warn`/`Command`)
- **Input** — goroutine + channel byte reader for async loops that select on
  input alongside other event sources (the channel-friendly counterpart to a
  blocking `f.Read`)
- **ReadEscape** / **EscapeKind** — normalized parser for cursor keys,
  page/home/end, and SGR mouse sequences (wheel mapped to scroll-up/down)
- **Draw helpers** — `WriteClipped` (move + clear-line + display-width-clipped
  write), `DrawBox` (titled bordered modal frame), `WrapPlain` (paragraph-aware
  wrap), `HeaderLine` (two-column status/help row)
- **Widgets** — `Overlay` (the surface widgets draw on), `Picker` (searchable
  scrollable list modal), `Prompt` (single-line text input modal)
- **Viewport** — scrollback window with `Scroll` / `Bottom` / `Visible`
- **Text engine** — `DisplayWidth` and `Wrap` honoring combining marks and
  East-Asian wide/fullwidth runes

## Install

```
go get github.com/zo-ll/tide
```

## Usage sketch

```go
term, _ := tide.Open(in, out)
term.EnterRaw()
term.EnterAltScreen()
term.EnableMouse()
defer term.Close()
defer term.WatchResize(redraw)()

in := tide.NewInput(term.In)
for {
    select {
    case <-resize:
        render(term)
    case b := <-inBytes(in):
        // dispatch bytes; on ESC use tide.ReadEscape(in.Next)
    }
}

// modal:
sel, ok := tide.NewPicker(tide.Overlay{
    Out: term.Out, Size: term.Size, Base: renderBase, Next: in.Next,
}).Open("choose", items)
```

## Scope

tide is deliberately not:
- a reactive/component framework
- a line editor (the old blocking `Editor` was removed; async apps build their
  own input handling on `Input` + `ReadEscape`)
- an app scaffold — there is no `App.Run`; your loop owns control flow

## Status

Small, stable core. One real consumer: [`oi`](https://github.com/zo-ll/oi).
`Picker`/`Prompt` are live-tested but not yet unit-tested with a byte harness.

## License

MIT
