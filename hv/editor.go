package main

import "fmt"

// Editor is a gap buffer: the document is split at the cursor into two stacks.
// left  holds the text before the cursor, in order.
// right holds the text after the cursor, REVERSED.
// So both characters adjacent to the cursor sit at the end of their slice,
// which makes insert / delete / single-step movement all O(1).
//
// Invariant: cursor position == len(left). It is derived, never stored.
type Editor struct {
	left  []rune
	right []rune

	// anchor is the fixed end of a selection (the mouse-down point).
	// -1 means no active selection. The moving end is the cursor, len(left).
	anchor int

	clipboard []rune

	undoStack []snapshot
	redoStack []snapshot
}

type snapshot struct {
	text   []rune
	cursor int
	anchor int
}

func NewEditor() *Editor {
	return &Editor{
		left:      make([]rune, 0),
		right:     make([]rune, 0),
		anchor:    -1,
		clipboard: make([]rune, 0),
	}
}

// ---------------------------------------------------------------- accessors

func (e *Editor) Len() int    { return len(e.left) + len(e.right) }
func (e *Editor) Cursor() int { return len(e.left) }

// Text renders the document. O(n) — the one place we pay for the reversal.
func (e *Editor) Text() string {
	out := make([]rune, 0, e.Len())
	out = append(out, e.left...)
	for i := len(e.right) - 1; i >= 0; i-- {
		out = append(out, e.right[i])
	}
	return string(out)
}

// ---------------------------------------------------------------- movement

func (e *Editor) clamp(pos int) int {
	if pos < 0 {
		return 0
	}
	if pos > e.Len() {
		return e.Len()
	}
	return pos
}

// moveGap shifts the split point without touching the selection.
// Cost is O(|delta|).
func (e *Editor) moveGap(pos int) {
	pos = e.clamp(pos)
	for len(e.left) > pos { // shift left: pop left, push right
		last := len(e.left) - 1
		e.right = append(e.right, e.left[last])
		e.left = e.left[:last]
	}
	for len(e.left) < pos { // shift right: pop right, push left
		last := len(e.right) - 1
		e.left = append(e.left, e.right[last])
		e.right = e.right[:last]
	}
}

// MoveTo places the cursor and clears any selection (a plain click deselects).
func (e *Editor) MoveTo(pos int) {
	e.moveGap(pos)
	e.anchor = -1
}

func (e *Editor) Left(n int)  { e.MoveTo(e.Cursor() - n) }
func (e *Editor) Right(n int) { e.MoveTo(e.Cursor() + n) }

// ---------------------------------------------------------------- selection

// Select marks a range. b is the moving end, so the cursor lands there —
// this is what a click-drag from a to b produces. b < a is legal.
func (e *Editor) Select(a, b int) {
	a, b = e.clamp(a), e.clamp(b)
	e.moveGap(b)
	if a == b {
		e.anchor = -1 // an empty selection is just a cursor
	} else {
		e.anchor = a
	}
}

func (e *Editor) hasSelection() bool {
	return e.anchor >= 0 && e.anchor != e.Cursor()
}

// selRange normalises a possibly-reversed selection into [lo, hi).
func (e *Editor) selRange() (int, int, bool) {
	if !e.hasSelection() {
		return 0, 0, false
	}
	lo, hi := e.anchor, e.Cursor()
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi, true
}

func (e *Editor) SelectedText() string {
	lo, hi, ok := e.selRange()
	if !ok {
		return ""
	}
	all := []rune(e.Text())
	return string(all[lo:hi])
}

// deleteSelection removes the selected range and leaves the cursor at its start.
// Reports whether anything was removed.
func (e *Editor) deleteSelection() bool {
	lo, hi, ok := e.selRange()
	if !ok {
		return false
	}
	e.moveGap(hi)                         // put the gap at the selection's end...
	e.left = e.left[:len(e.left)-(hi-lo)] // ...then drop hi-lo runes off the left
	e.anchor = -1
	return true
}

// ---------------------------------------------------------------- editing

func (e *Editor) Insert(s string) {
	e.pushUndo()
	e.deleteSelection() // typing replaces a selection
	for _, r := range s {
		e.left = append(e.left, r)
	}
}

// Backspace deletes the selection if there is one, else the char BEFORE the cursor.
func (e *Editor) Backspace() {
	e.pushUndo()
	if e.deleteSelection() {
		return
	}
	if len(e.left) > 0 {
		e.left = e.left[:len(e.left)-1]
	}
	// at position 0 this is a silent no-op, as in any real editor
}

// Delete (forward delete) removes the selection, else the char AT the cursor.
func (e *Editor) Delete() {
	e.pushUndo()
	if e.deleteSelection() {
		return
	}
	if len(e.right) > 0 {
		e.right = e.right[:len(e.right)-1]
	}
}

// ---------------------------------------------------------------- clipboard

func (e *Editor) Copy() {
	if _, _, ok := e.selRange(); !ok {
		return // copying nothing leaves the clipboard alone
	}
	e.clipboard = []rune(e.SelectedText())
}

func (e *Editor) Cut() {
	if _, _, ok := e.selRange(); !ok {
		return
	}
	e.pushUndo()
	e.clipboard = []rune(e.SelectedText())
	e.deleteSelection()
}

func (e *Editor) Paste() {
	e.pushUndo()
	e.deleteSelection() // paste replaces a selection
	e.left = append(e.left, e.clipboard...)
}

// ---------------------------------------------------------------- undo/redo

func (e *Editor) snap() snapshot {
	return snapshot{
		text:   []rune(e.Text()),
		cursor: e.Cursor(),
		anchor: e.anchor,
	}
}

func (e *Editor) restore(s snapshot) {
	e.left = append([]rune(nil), s.text[:s.cursor]...)
	e.right = make([]rune, 0, len(s.text)-s.cursor)
	for i := len(s.text) - 1; i >= s.cursor; i-- {
		e.right = append(e.right, s.text[i]) // rebuild reversed
	}
	e.anchor = s.anchor
}

// pushUndo is called by every mutating operation, before it mutates.
func (e *Editor) pushUndo() {
	e.undoStack = append(e.undoStack, e.snap())
	e.redoStack = e.redoStack[:0] // a fresh edit invalidates the redo branch
}

func (e *Editor) Undo() {
	if len(e.undoStack) == 0 {
		return
	}
	e.redoStack = append(e.redoStack, e.snap())
	top := len(e.undoStack) - 1
	e.restore(e.undoStack[top])
	e.undoStack = e.undoStack[:top]
}

func (e *Editor) Redo() {
	if len(e.redoStack) == 0 {
		return
	}
	e.undoStack = append(e.undoStack, e.snap())
	top := len(e.redoStack) - 1
	e.restore(e.redoStack[top])
	e.redoStack = e.redoStack[:top]
}

// ---------------------------------------------------------------- debug

// State renders the document with | at the cursor and [..] around a selection.
func (e *Editor) State() string {
	t := []rune(e.Text())
	if lo, hi, ok := e.selRange(); ok {
		return fmt.Sprintf("%s[%s]%s", string(t[:lo]), string(t[lo:hi]), string(t[hi:]))
	}
	c := e.Cursor()
	return string(t[:c]) + "|" + string(t[c:])
}
