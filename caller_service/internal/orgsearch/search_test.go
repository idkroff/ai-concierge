package orgsearch

import "testing"

func TestExtractPhone(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plus7 parens", "**+7 (495) 676-99-22** — телефон ресторана «Тануки»", "74956769922"},
		{"eight 8800 spaces", "Звоните 8 800 555 35 35 круглосуточно", "78005553535"},
		{"eight parens dashes", "тел: 8(495)739-00-33, адрес ...", "74957390033"},
		{"plus7 spaces", "Контакт: +7 495 676 99 22", "74956769922"},
		{"no phone", "Адрес: Волгоградский проспект, 17", ""},
		{"ten digits no code", "495 676 99 22", ""}, // без 7/8/+7 в начале не матчим как телефон
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractPhone(c.in); got != c.want {
				t.Fatalf("extractPhone(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFirstSentence(t *testing.T) {
	in := "**+7 (495) 676-99-22** — телефон ресторана «Тануки» в Таганском районе. Адрес: ..."
	want := "+7 (495) 676-99-22 — телефон ресторана «Тануки» в Таганском районе"
	if got := firstSentence(in); got != want {
		t.Fatalf("firstSentence = %q, want %q", got, want)
	}
}
