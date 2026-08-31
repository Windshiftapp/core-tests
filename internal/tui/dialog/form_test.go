//go:build test

package dialog

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"windshift/internal/tui/styles"
)

func TestFormSubmitKeysAvoidCtrlS(t *testing.T) {
	s := styles.New(styles.WindshiftDark())
	area := textarea.New()
	area.SetValue("hello")
	form := NewForm("comment", "Comment", []FormField{{Key: "body", Multiline: true, Area: area}}, s, 40)

	ctrlS := tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl})
	if action := form.HandleKey(ctrlS); action.Close || action.Selected != nil {
		t.Fatal("ctrl+s must remain available to the terminal multiplexer")
	}

	if action := form.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})); action.Close {
		t.Fatal("enter should begin editing the selected field")
	}
	altEnter := tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt})
	if action := form.HandleKey(altEnter); action.Close || action.Selected != nil {
		t.Fatal("alt+enter must not submit the form")
	}
	if action := form.HandleKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})); action.Close {
		t.Fatal("escape should stop field editing without closing the form")
	}

	action := form.HandleKey(tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"}))
	if !action.Close {
		t.Fatal("s should submit from field-selection mode")
	}
	result, ok := action.Selected.(FormResult)
	if !ok || result.Values["body"] != "hello" {
		t.Fatalf("unexpected form result: %#v", action.Selected)
	}
}

func TestFormChoiceConsumesPickerResult(t *testing.T) {
	s := styles.New(styles.WindshiftDark())
	choice := &FormChoice{
		PickerID: "status",
		Options:  []Option{{Label: "Open", Value: 1}, {Label: "Done", Value: 2}},
		Value:    1,
	}
	form := NewForm("edit", "Edit", []FormField{{Key: "status", Label: "Status", Choice: choice}}, s, 40)
	form.HandleResult(ResultMsg{ID: "status", Value: 2})
	action := form.HandleKey(tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"}))
	result := action.Selected.(FormResult)
	if got := result.Choices["status"]; got != 2 {
		t.Fatalf("picker result was not retained by the form: got %#v", got)
	}
}
