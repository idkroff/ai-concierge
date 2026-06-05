package orgsearch

import "testing"

func resolvePhone(text string) string { return normalize(rePhone.FindString(text)) }

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
		{"ten digits no code", "495 676 99 22", ""}, // без 7/8/+7 в начале не матчим
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolvePhone(c.in); got != c.want {
				t.Fatalf("resolvePhone(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestIsHotline(t *testing.T) {
	if !isHotline("78007707764") {
		t.Fatal("8-800 должен считаться федеральной линией")
	}
	if isHotline("74956769922") {
		t.Fatal("городской 495 не должен считаться федеральной линией")
	}
}

func TestDescribe(t *testing.T) {
	answer := "**+7 (495) 663-86-02** — номер телефона супермаркета «ВкусВилл» по адресу: ул. Арбат, 1, Москва. [1][2]"
	got := describe(answer, "+7 (495) 663-86-02")
	want := "супермаркета «ВкусВилл» по адресу: ул. Арбат, 1, Москва."
	if got != want {
		t.Fatalf("describe = %q, want %q", got, want)
	}
}

func TestBestContentSkipsNonObjects(t *testing.T) {
	raw := []byte(`["delta", {"message":{"content":"короткий"}}, {"message":{"content":"это самый длинный финальный ответ"}}]`)
	if got := bestContent(raw); got != "это самый длинный финальный ответ" {
		t.Fatalf("bestContent = %q", got)
	}
}
