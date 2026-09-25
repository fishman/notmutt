package theme

import "testing"

func TestResolveVariantAndInheritance(t *testing.T) {
	palette := Palette{
		Base:     map[string]string{"fg": "#101010", "bg": "#202020"},
		Variants: map[string]map[string]string{"light": {"fg": "#eeeeee"}},
	}
	styles := map[string]Style{
		"normal":       {Fg: "fg", Bg: "bg", Attrs: []string{"bold"}},
		"status":       {Fg: "fg", Attrs: []string{"underline"}},
		"queue.header": {Fg: "#abcdef"},
	}
	got := Resolve(palette, "light", styles)
	if got["normal"].Fg != "#eeeeee" || got["normal"].Bg != "#202020" {
		t.Fatalf("normal = %+v", got["normal"])
	}
	if status := got["status"]; status.Fg != "#eeeeee" || status.Bg != "#202020" || len(status.Attrs) != 1 || status.Attrs[0] != "underline" {
		t.Fatalf("status = %+v", status)
	}
	if header := got["queue.header"]; header.Fg != "#abcdef" || header.Bg != "#202020" || len(header.Attrs) != 1 || header.Attrs[0] != "bold" {
		t.Fatalf("queue.header = %+v", header)
	}
	got["queue.header"].Attrs[0] = "mutated"
	if styles["normal"].Attrs[0] != "bold" {
		t.Fatal("resolved attrs alias normal style")
	}
}

func TestValidHex(t *testing.T) {
	for _, color := range []string{"#00AAbb", "#123456"} {
		if !ValidHex(color) {
			t.Fatalf("valid color %q rejected", color)
		}
	}
	for _, color := range []string{"#abc", "#GG0000", "123456", "#abcdef0"} {
		if ValidHex(color) {
			t.Fatalf("invalid color %q accepted", color)
		}
	}
}
