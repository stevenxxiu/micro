package main

import (
	"github.com/atotto/clipboard"
	"github.com/gdamore/tcell"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

// The View struct stores information about a view into a buffer.
// It has a value for the cursor, and the window that the user sees
// the buffer from.
type View struct {
	cursor  Cursor
	topline int
	// Leftmost column. Used for horizontal scrolling
	leftCol int

	// Percentage of the terminal window that this view takes up
	heightPercent float32
	widthPercent  float32
	height        int
	width         int

	// How much to offset because of line numbers
	lineNumOffset int

	eh *EventHandler

	buf *Buffer
	sl  Statusline

	mouseReleased bool

	// Syntax highlighting matches
	matches map[int]tcell.Style

	m *Messenger

	s tcell.Screen
}

// NewView returns a new view with fullscreen width and height
func NewView(buf *Buffer, m *Messenger, s tcell.Screen) *View {
	return NewViewWidthHeight(buf, m, s, 1, 1)
}

// NewViewWidthHeight returns a new view with the specified width and height percentages
func NewViewWidthHeight(buf *Buffer, m *Messenger, s tcell.Screen, w, h float32) *View {
	v := new(View)

	v.buf = buf
	v.s = s
	v.m = m

	v.widthPercent = w
	v.heightPercent = h
	v.Resize(s.Size())

	v.topline = 0
	v.cursor = Cursor{
		x:   0,
		y:   0,
		loc: 0,
		v:   v,
	}

	v.eh = NewEventHandler(v)

	v.sl = Statusline{
		v: v,
	}

	return v
}

// Resize recalculates the width and height of the view based on the width and height percentages
func (v *View) Resize(w, h int) {
	h--
	v.height = int(float32(h)*v.heightPercent) - 1
	v.width = int(float32(w) * v.widthPercent)
}

// ScrollUp scrolls the view up n lines (if possible)
func (v *View) ScrollUp(n int) {
	// Try to scroll by n but if it would overflow, scroll by 1
	if v.topline-n >= 0 {
		v.topline -= n
	} else if v.topline > 0 {
		v.topline--
	}
}

// ScrollDown scrolls the view down n lines (if possible)
func (v *View) ScrollDown(n int) {
	// Try to scroll by n but if it would overflow, scroll by 1
	if v.topline+n <= len(v.buf.lines)-v.height {
		v.topline += n
	} else if v.topline < len(v.buf.lines)-v.height {
		v.topline++
	}
}

// PageUp scrolls the view up a page
func (v *View) PageUp() {
	if v.topline > v.height {
		v.ScrollUp(v.height)
	} else {
		v.topline = 0
	}
}

// PageDown scrolls the view down a page
func (v *View) PageDown() {
	if len(v.buf.lines)-(v.topline+v.height) > v.height {
		v.ScrollDown(v.height)
	} else {
		v.topline = len(v.buf.lines) - v.height
	}
}

// HalfPageUp scrolls the view up half a page
func (v *View) HalfPageUp() {
	if v.topline > v.height/2 {
		v.ScrollUp(v.height / 2)
	} else {
		v.topline = 0
	}
}

// HalfPageDown scrolls the view down half a page
func (v *View) HalfPageDown() {
	if len(v.buf.lines)-(v.topline+v.height) > v.height/2 {
		v.ScrollDown(v.height / 2)
	} else {
		v.topline = len(v.buf.lines) - v.height
	}
}

// HandleEvent handles an event passed by the main loop
// It returns an int describing how the screen needs to be redrawn
// 0: Screen does not need to be redrawn
// 1: Only the cursor/statusline needs to be redrawn
// 2: Everything needs to be redrawn
func (v *View) HandleEvent(event tcell.Event) int {
	var ret int
	switch e := event.(type) {
	case *tcell.EventResize:
		// Window resized
		v.Resize(e.Size())
		ret = 2
	case *tcell.EventKey:
		switch e.Key() {
		case tcell.KeyCtrlQ:
			// Quit
			if v.buf.IsDirty() {
				quit, canceled := v.m.Prompt("You have unsaved changes. Quit anyway? ")
				if !canceled {
					if strings.ToLower(quit) == "yes" || strings.ToLower(quit) == "y" {
						v.s.Fini()
						os.Exit(0)
					} else {
						return 2
					}
				} else {
					return 2
				}
			} else {
				v.s.Fini()
				os.Exit(0)
			}
		case tcell.KeyUp:
			// Cursor up
			v.cursor.Up()
			ret = 1
		case tcell.KeyDown:
			// Cursor down
			v.cursor.Down()
			ret = 1
		case tcell.KeyLeft:
			// Cursor left
			v.cursor.Left()
			ret = 1
		case tcell.KeyRight:
			// Cursor right
			v.cursor.Right()
			ret = 1
		case tcell.KeyEnter:
			// Insert a newline
			v.eh.Insert(v.cursor.loc, "\n")
			v.cursor.Right()
			ret = 2
		case tcell.KeySpace:
			// Insert a space
			v.eh.Insert(v.cursor.loc, " ")
			v.cursor.Right()
			ret = 2
		case tcell.KeyBackspace2:
			// Delete a character
			if v.cursor.HasSelection() {
				v.cursor.DeleteSelection()
				v.cursor.ResetSelection()
				ret = 2
			} else if v.cursor.loc > 0 {
				// We have to do something a bit hacky here because we want to
				// delete the line by first moving left and then deleting backwards
				// but the undo redo would place the cursor in the wrong place
				// So instead we move left, save the position, move back, delete
				// and restore the position
				v.cursor.Left()
				cx, cy, cloc := v.cursor.x, v.cursor.y, v.cursor.loc
				v.cursor.Right()
				v.eh.Remove(v.cursor.loc-1, v.cursor.loc)
				v.cursor.x, v.cursor.y, v.cursor.loc = cx, cy, cloc
				ret = 2
			}
		case tcell.KeyTab:
			// Insert a tab
			v.eh.Insert(v.cursor.loc, "\t")
			v.cursor.Right()
			ret = 2
		case tcell.KeyCtrlS:
			// Save
			if v.buf.path == "" {
				filename, canceled := v.m.Prompt("Filename: ")
				if !canceled {
					v.buf.path = filename
					v.buf.name = filename
				} else {
					return 2
				}
			}
			err := v.buf.Save()
			if err != nil {
				v.m.Error(err.Error())
			}
			// Need to redraw the status line
			ret = 1
		case tcell.KeyCtrlZ:
			// Undo
			v.eh.Undo()
			ret = 2
		case tcell.KeyCtrlY:
			// Redo
			v.eh.Redo()
			ret = 2
		case tcell.KeyCtrlC:
			// Copy
			if v.cursor.HasSelection() {
				if !clipboard.Unsupported {
					clipboard.WriteAll(v.cursor.GetSelection())
					ret = 2
				}
			}
		case tcell.KeyCtrlX:
			// Cut
			if v.cursor.HasSelection() {
				if !clipboard.Unsupported {
					clipboard.WriteAll(v.cursor.GetSelection())
					v.cursor.DeleteSelection()
					v.cursor.ResetSelection()
					ret = 2
				}
			}
		case tcell.KeyCtrlV:
			// Paste
			if !clipboard.Unsupported {
				if v.cursor.HasSelection() {
					v.cursor.DeleteSelection()
					v.cursor.ResetSelection()
				}
				clip, _ := clipboard.ReadAll()
				v.eh.Insert(v.cursor.loc, clip)
				// This is a bit weird... Not sure if there's a better way
				for i := 0; i < Count(clip); i++ {
					v.cursor.Right()
				}
				ret = 2
			}
		case tcell.KeyCtrlA:
			// Select all
			v.cursor.selectionEnd = 0
			v.cursor.selectionStart = v.buf.Len()
			v.cursor.x = 0
			v.cursor.y = 0
			v.cursor.loc = 0
			ret = 2
		case tcell.KeyCtrlO:
			// Open file
			if v.buf.IsDirty() {
				quit, canceled := v.m.Prompt("You have unsaved changes. Continue? ")
				if !canceled {
					if strings.ToLower(quit) == "yes" || strings.ToLower(quit) == "y" {
						return v.OpenFile()
					} else {
						return 2
					}
				} else {
					return 2
				}
			} else {
				return v.OpenFile()
			}
		case tcell.KeyPgUp:
			// Page up
			v.PageUp()
			return 2
		case tcell.KeyPgDn:
			// Page down
			v.PageDown()
			return 2
		case tcell.KeyCtrlU:
			// Half page up
			v.HalfPageUp()
			return 2
		case tcell.KeyCtrlD:
			// Half page down
			v.HalfPageDown()
			return 2
		case tcell.KeyRune:
			// Insert a character
			if v.cursor.HasSelection() {
				v.cursor.DeleteSelection()
				v.cursor.ResetSelection()
			}
			v.eh.Insert(v.cursor.loc, string(e.Rune()))
			v.cursor.Right()
			ret = 2
		}
	case *tcell.EventMouse:
		x, y := e.Position()
		x -= v.lineNumOffset
		y += v.topline
		// Position always seems to be off by one
		x--
		y--

		button := e.Buttons()

		switch button {
		case tcell.Button1:
			// Left click
			if y-v.topline > v.height-1 {
				v.ScrollDown(1)
				y = v.height + v.topline - 1
			}
			if y >= len(v.buf.lines) {
				y = len(v.buf.lines) - 1
			}
			if x < 0 {
				x = 0
			}

			x = v.cursor.GetCharPosInLine(y, x)
			if x > Count(v.buf.lines[y]) {
				x = Count(v.buf.lines[y])
			}
			d := v.cursor.Distance(x, y)
			v.cursor.loc += d
			v.cursor.x = x
			v.cursor.y = y

			if v.mouseReleased {
				v.cursor.selectionStart = v.cursor.loc
				v.cursor.selectionStartX = v.cursor.x
				v.cursor.selectionStartY = v.cursor.y
			}
			v.cursor.selectionEnd = v.cursor.loc
			v.mouseReleased = false
			return 2
		case tcell.ButtonNone:
			// Mouse event with no click
			v.mouseReleased = true
			// We need to directly return here because otherwise the view will
			// be readjusted to put the cursor in it, but there may be mouse events
			// where the cursor is not (and should not be) be involved
			return 0
		case tcell.WheelUp:
			// Scroll up two lines
			v.ScrollUp(2)
			return 2
		case tcell.WheelDown:
			// Scroll down two lines
			v.ScrollDown(2)
			return 2
		}
	}

	// Reset the view so the cursor is in view
	cy := v.cursor.y
	if cy < v.topline {
		v.topline = cy
		ret = 2
	}
	if cy > v.topline+v.height-1 {
		v.topline = cy - v.height + 1
		ret = 2
	}

	return ret
}

// OpenFile Prompts the user for a filename and opens the file in the current buffer
func (v *View) OpenFile() int {
	filename, canceled := v.m.Prompt("File to open: ")
	if canceled {
		return 2
	}
	file, err := ioutil.ReadFile(filename)

	if err != nil {
		v.m.Error(err.Error())
		return 2
	}
	v.buf = NewBuffer(string(file), filename)
	return 2
}

// Display renders the view to the screen
func (v *View) Display() {
	var x int

	charNum := v.cursor.loc + v.cursor.Distance(0, v.topline)

	// Convert the length of buffer to a string, and get the length of the string
	// We are going to have to offset by that amount
	maxLineLength := len(strconv.Itoa(len(v.buf.lines)))
	// + 1 for the little space after the line number
	v.lineNumOffset = maxLineLength + 1

	var highlightStyle tcell.Style

	for lineN := 0; lineN < v.height; lineN++ {
		if lineN+v.topline >= len(v.buf.lines) {
			break
		}
		line := v.buf.lines[lineN+v.topline]

		// Write the line number
		lineNumStyle := tcell.StyleDefault
		if _, ok := colorscheme["line-number"]; ok {
			lineNumStyle = colorscheme["line-number"]
		}
		// Write the spaces before the line number if necessary
		lineNum := strconv.Itoa(lineN + v.topline + 1)
		for i := 0; i < maxLineLength-len(lineNum); i++ {
			v.s.SetContent(x, lineN, ' ', nil, lineNumStyle)
			x++
		}
		// Write the actual line number
		for _, ch := range lineNum {
			v.s.SetContent(x, lineN, ch, nil, lineNumStyle)
			x++
		}
		// Write the extra space
		v.s.SetContent(x, lineN, ' ', nil, lineNumStyle)
		x++

		// Write the line
		tabchars := 0
		for _, ch := range line {
			var lineStyle tcell.Style
			st, ok := v.matches[charNum]
			if ok {
				highlightStyle = st
			} else {
				highlightStyle = tcell.StyleDefault
			}

			if v.cursor.HasSelection() &&
				(charNum >= v.cursor.selectionStart && charNum <= v.cursor.selectionEnd ||
					charNum <= v.cursor.selectionStart && charNum >= v.cursor.selectionEnd) {

				lineStyle = tcell.StyleDefault.Reverse(true)

				if _, ok := colorscheme["selection"]; ok {
					lineStyle = colorscheme["selection"]
				}
			} else {
				lineStyle = highlightStyle
			}

			if ch == '\t' {
				v.s.SetContent(x+tabchars, lineN, ' ', nil, lineStyle)
				for i := 0; i < tabSize-1; i++ {
					tabchars++
					v.s.SetContent(x+tabchars, lineN, ' ', nil, lineStyle)
				}
			} else {
				v.s.SetContent(x+tabchars, lineN, ch, nil, lineStyle)
			}
			charNum++
			x++
		}
		if v.cursor.HasSelection() &&
			(charNum >= v.cursor.selectionStart && charNum <= v.cursor.selectionEnd ||
				charNum <= v.cursor.selectionStart && charNum >= v.cursor.selectionEnd) {

			selectStyle := tcell.StyleDefault.Reverse(true)

			if _, ok := colorscheme["selection"]; ok {
				selectStyle = colorscheme["selection"]
			}
			v.s.SetContent(x+tabchars, lineN, ' ', nil, selectStyle)
		}

		x = 0
		st, ok := v.matches[charNum]
		if ok {
			highlightStyle = st
		}
		charNum++
	}
}