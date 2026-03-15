package formatter

import (
	"testing"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "already formatted",
			input: "behavior foo {\n    @name \"Hello\"\n    exit\n}\n",
			want:  "behavior foo {\n    @name \"Hello\"\n    exit\n}\n",
		},
		{
			name:  "fix indentation",
			input: "behavior foo {\nexit\n}\n",
			want:  "behavior foo {\n    exit\n}\n",
		},
		{
			name:  "nested indentation",
			input: "behavior foo {\nif true {\nexit\n}\n}\n",
			want:  "behavior foo {\n    if true {\n        exit\n    }\n}\n",
		},
		{
			name:  "unlocked block indentation",
			input: "behavior foo {\nunlocked {\n# comment\nlet x = 1\n}\n}\n",
			want:  "behavior foo {\n    unlocked {\n        # comment\n        let x = 1\n    }\n}\n",
		},
		{
			name:  "operator spacing",
			input: "let x=1+2*3\n",
			want:  "let x = 1 + 2 * 3\n",
		},
		{
			name:  "comparison spacing",
			input: "if x>5&&y<=10 { exit }\n",
			want:  "if x > 5 && y <= 10 { exit }\n",
		},
		{
			name:  "compound assignment spacing",
			input: "x+=1\ny-=2\nz*=3\nw/=4\nv%=5\n",
			want:  "x += 1\ny -= 2\nz *= 3\nw /= 4\nv %= 5\n",
		},
		{
			name:  "increment decrement no space",
			input: "x ++\ny --\n",
			want:  "x++\ny--\n",
		},
		{
			name:  "unary minus",
			input: "let x = - 5\nlet y = x + - 3\n",
			want:  "let x = -5\nlet y = x + -3\n",
		},
		{
			name:  "binary minus",
			input: "let x = a-b\n",
			want:  "let x = a - b\n",
		},
		{
			name:  "faction register prefix",
			input: "% counter += 1\nlet x = % counter\n",
			want:  "%counter += 1\nlet x = %counter\n",
		},
		{
			name:  "modulo operator",
			input: "let x = a%b\n",
			want:  "let x = a % b\n",
		},
		{
			name:  "function call no space before paren",
			input: "foo (1, 2)\n",
			want:  "foo(1, 2)\n",
		},
		{
			name:  "constructor no space before paren",
			input: "let x = Item (\"metalbar\")\n",
			want:  "let x = Item(\"metalbar\")\n",
		},
		{
			name:  "block keyword space before paren",
			input: "if(x > 5) { exit }\n",
			want:  "if (x > 5) { exit }\n",
		},
		{
			name:  "comma spacing",
			input: "foo(a ,b ,c)\n",
			want:  "foo(a, b, c)\n",
		},
		{
			name:  "colon spacing",
			input: "foo(a, key : val)\n",
			want:  "foo(a, key: val)\n",
		},
		{
			name:  "dot access no space",
			input: "let x = a . number\n",
			want:  "let x = a.number\n",
		},
		{
			name:  "double colon no space",
			input: "let x = Color :: Red\n",
			want:  "let x = Color::Red\n",
		},
		{
			name:  "bang no space",
			input: "if ! x { exit }\n",
			want:  "if !x { exit }\n",
		},
		{
			name:  "not is",
			input: "if x ! is Item { exit }\n",
			want:  "if x !is Item { exit }\n",
		},
		{
			name:  "at directive no space",
			input: "@ name \"Hello\"\n@ param in x \"X\"\n",
			want:  "@name \"Hello\"\n@param in x \"X\"\n",
		},
		{
			name:  "empty block stays tight",
			input: "loop {  }\n",
			want:  "loop {}\n",
		},
		{
			name:  "trailing semicolon stripped",
			input: "let x = 1;\nlet y = 2;\n",
			want:  "let x = 1\nlet y = 2\n",
		},
		{
			name:  "midline semicolon preserved",
			input: "let x = 1; exit\n",
			want:  "let x = 1; exit\n",
		},
		{
			name:  "semicolon before closing brace stripped",
			input: "if true { exit; }\n",
			want:  "if true { exit }\n",
		},
		{
			name:  "collapse multiple blank lines",
			input: "let x = 1\n\n\n\nlet y = 2\n",
			want:  "let x = 1\n\nlet y = 2\n",
		},
		{
			name:  "preserve single blank line",
			input: "let x = 1\n\nlet y = 2\n",
			want:  "let x = 1\n\nlet y = 2\n",
		},
		{
			name:  "ampersand composite",
			input: "let x = Item(\"metalbar\")&5\n",
			want:  "let x = Item(\"metalbar\") & 5\n",
		},
		{
			name:  "arrow spacing",
			input: "big { v, flag->body }\n",
			want:  "big { v, flag -> body }\n",
		},
		{
			name:  "label and colon",
			input: "'outer : loop { break 'outer }\n",
			want:  "'outer: loop { break 'outer }\n",
		},
		{
			name:  "comment preserved",
			input: "# This is a comment\nlet x = 1 # inline\n",
			want:  "# This is a comment\nlet x = 1 # inline\n",
		},
		{
			name:  "trailing newline added",
			input: "exit",
			want:  "exit\n",
		},
		{
			name:  "import statement",
			input: "import * from \"./lib\"\n",
			want:  "import * from \"./lib\"\n",
		},
		{
			name:  "enum declaration",
			input: "enum Color{Red,Green,Blue}\n",
			want:  "enum Color { Red, Green, Blue }\n",
		},
		{
			name: "exec declaration spacing",
			input: "fn classify(a) exec(big, small) {\nreturn big\n}\n",
			want:  "fn classify(a) exec(big, small) {\n    return big\n}\n",
		},
		{
			name:  "else if on same line",
			input: "} else if x>5 {\n",
			want:  "} else if x > 5 {\n",
		},
		{
			name:  "wait statement",
			input: "wait 100\n",
			want:  "wait 100\n",
		},
		{
			name: "on event handler",
			input: "on $trigger {\n$result+=1\n}\n",
			want:  "on $trigger {\n    $result += 1\n}\n",
		},
		{
			name:  "assert statement",
			input: "assert $result>0\n",
			want:  "assert $result > 0\n",
		},
		{
			name:  "boolean operators",
			input: "if a||b&&!c { exit }\n",
			want:  "if a || b && !c { exit }\n",
		},
		{
			name:  "equality operators",
			input: "if a==b&&c!=d { exit }\n",
			want:  "if a == b && c != d { exit }\n",
		},
		{
			name:  "is type check",
			input: "if x is Item { exit }\n",
			want:  "if x is Item { exit }\n",
		},
		{
			name:  "label before paren no space",
			input: "exec 0: 'larger (@1)\n",
			want:  "exec 0: 'larger(@1)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Format(tt.input)
			if err != nil {
				t.Fatalf("Format() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("Format() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestIdempotent(t *testing.T) {
	// Formatting an already-formatted file should produce the same output.
	input := `behavior test {
    @name "Test"
    @param inout result "Result"

    $result = 0

    unlocked {
        # arithmetic
        let x = 3 + 4
        if x == 7 { $result += 1 } else { $failed = 1; exit }

        # function call
        let y = foo(1, 2, key: 3)
    }

    %counter += 1
    let c = %counter

    'outer: loop {
        if c > 10 { break 'outer }
        c++
    }

    exit
}
`
	got, err := Format(input)
	if err != nil {
		t.Fatalf("Format() error: %v", err)
	}
	if got != input {
		t.Errorf("not idempotent:\ninput:\n%s\noutput:\n%s", input, got)
	}
}
