package extract

import (
	"testing"
)

func TestFormExtractor_RequiredFieldDefaultingAndHiddenSubmitSplit(t *testing.T) {
	body := `<html><body>
		<form action="/signup" method="post">
			<input type="hidden" name="csrf" value="tok">
			<input type="email" name="email" required>
			<input type="text" name="nickname">
			<input type="submit" name="go" value="Sign up">
			<button type="submit" formaction="/other">skip</button>
		</form>
	</body></html>`
	discoveries := parseAndExtract(t, body, "https://example.com/", FormExtractor{})
	if len(discoveries) != 1 {
		t.Fatalf("expected 1 form, got %d: %+v", len(discoveries), discoveries)
	}
	f := discoveries[0]
	if f.URL != "https://example.com/signup" || f.Method != "POST" {
		t.Errorf("unexpected form target: url=%q method=%q", f.URL, f.Method)
	}

	params := map[string]string{}
	for _, p := range f.Params {
		params[p.Name] = p.Value
	}
	if params["email"] != "test@example.com" {
		t.Errorf("required email field should default to test@example.com, got %q", params["email"])
	}
	if v, ok := params["nickname"]; !ok || v != "" {
		t.Errorf("non-required nickname field should be present with empty value, got %q (present=%v)", v, ok)
	}
	if _, ok := params["csrf"]; ok {
		t.Errorf("hidden field csrf must not appear in injectable params")
	}
	if _, ok := params["go"]; ok {
		t.Errorf("submit field 'go' must not appear in injectable params")
	}
	if f.Base["csrf"] != "tok" {
		t.Errorf("hidden field csrf should be in base_data with value 'tok', got %q", f.Base["csrf"])
	}
}

func TestFormExtractor_DropsFormsWithNoInjectableParams(t *testing.T) {
	body := `<html><body>
		<form action="/ping" method="get">
			<input type="hidden" name="csrf" value="tok">
			<input type="submit" value="go">
		</form>
	</body></html>`
	discoveries := parseAndExtract(t, body, "https://example.com/", FormExtractor{})
	if len(discoveries) != 0 {
		t.Errorf("form with only hidden/submit fields should be dropped, got %+v", discoveries)
	}
}

func TestFormExtractor_SelectUsesSelectedOptionOrFallsBackToFirst(t *testing.T) {
	body := `<html><body>
		<form action="/pick" method="get">
			<select name="color">
				<option value="red">Red</option>
				<option value="blue" selected>Blue</option>
			</select>
			<select name="size">
				<option value="s">S</option>
				<option value="m">M</option>
			</select>
		</form>
	</body></html>`
	discoveries := parseAndExtract(t, body, "https://example.com/", FormExtractor{})
	if len(discoveries) != 1 {
		t.Fatalf("expected 1 form, got %d", len(discoveries))
	}
	params := map[string]string{}
	for _, p := range discoveries[0].Params {
		params[p.Name] = p.Value
	}
	if params["color"] != "blue" {
		t.Errorf("color should use the selected option 'blue', got %q", params["color"])
	}
	if params["size"] != "s" {
		t.Errorf("size has no selected option, should fall back to first ('s'), got %q", params["size"])
	}
}
