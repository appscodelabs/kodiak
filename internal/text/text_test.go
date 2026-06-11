package text

import "testing"

func TestStripHTMLCommentsFromMarkdown(t *testing.T) {
	cases := []struct {
		original string
		stripped string
	}{
		{
			original: "Non dolor velit vel quia mollitia. Placeat cumque a deleniti possimus.\n\nTotam dolor [exercitationem laborum](https://numquam.com)\n\n<!--\n- Voluptatem voluptas officiis\n- Voluptates nulla tempora\n- Officia distinctio ut ab \U0001F47E\n  + Est ut voluptatum consequuntur recusandae aspernatur\n  + Quidem debitis atque dolorum est enim\n-->\n",
			stripped: "Non dolor velit vel quia mollitia. Placeat cumque a deleniti possimus.\n\nTotam dolor [exercitationem laborum](https://numquam.com)\n\n\n",
		},
		{
			original: "Non dolor velit vel quia mollitia.\r\n\r\nVoluptates nulla tempora.\r\n\r\n<!--\r\n- Voluptatem voluptas officiis\r\n- Voluptates nulla tempora\r\n- Officia distinctio ut ab\r\n  + \"Est ut voluptatum\" consequuntur recusandae aspernatur\r\n  + Quidem debitis atque dolorum est enim\r\n-->",
			stripped: "Non dolor velit vel quia mollitia.\n\nVoluptates nulla tempora.\n\n",
		},
		{
			original: "hello <!-- testing \U0001F47E -->world",
			stripped: "hello world",
		},
		{
			original: "hello <span>  <p>  <!-- testing \U0001F47E --> hello</p></span>world",
			stripped: "hello <span>  <p>   hello</p></span>world",
		},
		{
			original: "hello <span>  <p>  <!-- testing \U0001F47E --> hello<!-- 123 \U0001F47E --></p></span>world",
			stripped: "hello <span>  <p>   hello</p></span>world",
		},
		{
			original: "this is an example comment message with a comment from a PR template\n\n<!--\n- bullet one\n- bullet two \U0001F47E\n- bullet three\n  + sub bullet one\n  + sub bullet two\n-->\n",
			stripped: "this is an example comment message with a comment from a PR template\n\n\n",
		},
		{
			original: "\U0001F3F7️<!-- -->",
			stripped: "\U0001F3F7️",
		},
	}
	for i, c := range cases {
		got := StripHTMLCommentsFromMarkdown(c.original)
		if got != c.stripped {
			t.Errorf("case %d:\noriginal: %q\nexpected: %q\ngot:      %q", i, c.original, c.stripped, got)
		}
	}
}
