//go:build test

package app

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"windshift/internal/tui/core"
	"windshift/internal/tui/dialog"
	"windshift/internal/tui/styles"
)

type stubScreen struct{}

func (stubScreen) Init() tea.Cmd            { return nil }
func (stubScreen) Update(tea.Msg) tea.Cmd   { return nil }
func (stubScreen) View() string             { return "BOARD-CONTEXT" }
func (stubScreen) SetSize(int, int)         {}
func (stubScreen) Title() string            { return "Board" }
func (stubScreen) ShortHelp() []key.Binding { return nil }

type resultDialog struct{ got any }

func (d *resultDialog) ID() string                              { return "parent" }
func (d *resultDialog) Title() string                           { return "Parent" }
func (d *resultDialog) HandleKey(tea.KeyPressMsg) dialog.Action { return dialog.Action{} }
func (d *resultDialog) View(int, int) string                    { return "parent" }
func (d *resultDialog) HandleResult(msg dialog.ResultMsg) tea.Cmd {
	d.got = msg.Value
	return nil
}

func TestThemeKeyOpensPicker(t *testing.T) {
	ctx := testContext()
	m := New(ctx, stubScreen{})
	updated, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: 't', Text: "t"}))
	got := updated.(Model)
	if len(got.dialogs) != 1 || got.dialogs[0].ID() != themePickerID {
		t.Fatalf("theme key did not open the theme picker: %#v", got.dialogs)
	}
}

func TestOverlayRetainsApplicationContext(t *testing.T) {
	ctx := testContext()
	m := New(ctx, stubScreen{})
	picker := dialog.NewPicker("test", "Choose", []dialog.Option{{Label: "One", Value: 1}}, 0, ctx.Styles)
	content := strings.Repeat("background row\n", 20)
	got := m.overlayDialog(content, picker)
	if !strings.Contains(got, "background row") || !strings.Contains(got, "Choose") {
		t.Fatalf("overlay should contain both background and dialog, got %q", got)
	}
}

func TestChildPickerReturnsResultToParentDialog(t *testing.T) {
	ctx := testContext()
	m := New(ctx, stubScreen{})
	parent := &resultDialog{}
	child := dialog.NewPicker("child", "Child", []dialog.Option{{Label: "Selected", Value: 42}}, 0, ctx.Styles)
	m.dialogs = []dialog.Dialog{parent, child}

	updated, _ := m.handleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := updated.(Model)
	if len(got.dialogs) != 1 || got.dialogs[0] != parent {
		t.Fatalf("child picker should close back to its parent: %#v", got.dialogs)
	}
	if parent.got != 42 {
		t.Fatalf("picker result did not reach parent dialog: %#v", parent.got)
	}
}

func testContext() *core.Ctx {
	return &core.Ctx{
		Styles: styles.New(styles.WindshiftDark()),
		Theme:  styles.DefaultTheme,
		Keys:   core.DefaultKeyMap(),
		Width:  80,
		Height: 24,
	}
}
