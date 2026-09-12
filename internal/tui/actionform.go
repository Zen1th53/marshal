package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxActionInputRunes = 4096

// InputSpec is one closed, typed field collected for a canonical action.
// There is deliberately no generic command/shell kind.
type InputSpec struct {
	Key       string
	Label     string
	Required  bool
	Sensitive bool
	Default   string
	Validate  func(string) error
}

type actionForm struct {
	binding Binding
	request ActionRequest
	values  map[string]string
	index   int
	open    bool
	err     string
}

func newActionForm(binding Binding, request ActionRequest) *actionForm {
	values := make(map[string]string, len(binding.Inputs))
	for _, field := range binding.Inputs {
		values[field.Key] = field.Default
	}
	return &actionForm{binding: binding, request: request, values: values, open: true}
}

func (f *actionForm) current() (InputSpec, bool) {
	if f == nil || !f.open || f.index < 0 || f.index >= len(f.binding.Inputs) {
		return InputSpec{}, false
	}
	return f.binding.Inputs[f.index], true
}

func (f *actionForm) append(text string) {
	field, ok := f.current()
	if !ok {
		return
	}
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	joined := []rune(f.values[field.Key] + text)
	if len(joined) > maxActionInputRunes {
		joined = joined[:maxActionInputRunes]
	}
	f.values[field.Key] = string(joined)
	f.err = ""
}

func (f *actionForm) backspace() {
	field, ok := f.current()
	if !ok {
		return
	}
	value := f.values[field.Key]
	if value == "" {
		return
	}
	_, size := utf8.DecodeLastRuneInString(value)
	f.values[field.Key] = value[:len(value)-size]
	f.err = ""
}

func (f *actionForm) move(delta int) {
	if f == nil || !f.open || len(f.binding.Inputs) == 0 {
		return
	}
	f.index += delta
	if f.index < 0 {
		f.index = 0
	}
	if f.index >= len(f.binding.Inputs) {
		f.index = len(f.binding.Inputs) - 1
	}
	f.err = ""
}

func (f *actionForm) build() (ActionRequest, error) {
	if f == nil || !f.open {
		return ActionRequest{}, fmt.Errorf("tui: no action form is open")
	}
	for i, field := range f.binding.Inputs {
		value := strings.TrimSpace(f.values[field.Key])
		if field.Required && value == "" {
			f.index = i
			f.err = field.Label + " is required"
			return ActionRequest{}, fmt.Errorf("%s", f.err)
		}
		if field.Validate != nil {
			if err := field.Validate(value); err != nil {
				f.index = i
				f.err = err.Error()
				return ActionRequest{}, err
			}
		}
		f.values[field.Key] = value
	}
	req := f.request
	req.Inputs = make(map[string]string, len(f.values))
	for key, value := range f.values {
		req.Inputs[key] = value
	}
	return req, nil
}

func (f *actionForm) cancel() { f.open = false }
