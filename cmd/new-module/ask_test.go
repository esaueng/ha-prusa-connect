package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/scaffold"
	"github.com/prusa3d/prusa-connect/sdk/manifest"
)

// answering is an asker whose person types lines, one per question, and then
// stops typing.
func answering(out *bytes.Buffer, lines ...string) *asker {
	typed := ""
	if len(lines) > 0 {
		typed = strings.Join(lines, "\n") + "\n"
	}

	return &asker{out: out, in: bufio.NewReader(strings.NewReader(typed))}
}

// TestAMenuAsksAgainForWhatIsNotOneOfItsNumbers. A menu takes one of its own
// numbers, and anything else is asked again rather than read as the default.
func TestAMenuAsksAgainForWhatIsNotOneOfItsNumbers(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		typed []string
		want  int
	}{
		"a number":                    {[]string{"2"}, 2},
		"Enter takes the default":     {[]string{""}, 3},
		"out of range, then a number": {[]string{"4", "1"}, 1},
		"zero, then a number":         {[]string{"0", "1"}, 1},
		"a word, then a number":       {[]string{"two", "2"}, 2},
		"two numbers, then one":       {[]string{"1,2", "1"}, 1},
		"the stream ends":             {nil, 3},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			a := answering(&out, c.typed...)
			require.Equal(t, c.want, a.choose(question{"Which?", "One of them."}, []string{"a", "b", "c"}, 3))
		})
	}
}

// TestAQuestionPrintsItsHintBeneathIt. The hint is what tells a person what an
// answer is used for, so it is printed with every question.
func TestAQuestionPrintsItsHintBeneathIt(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	answering(&out, "1").choose(questions["kind"], shapeOffers(), 1)

	require.True(t, strings.HasPrefix(out.String(),
		"\n? What kind of module is it?\n"+
			"  A UI adds something to the app's screens. A process is a program the server runs for the module.\n"+
			"  1) UI only: "), out.String())
	require.Contains(t, out.String(), "› [1] ")
}

// TestEveryQuestionHasAHint. A question without one is a question a person
// answers without knowing what the answer does.
func TestEveryQuestionHasAHint(t *testing.T) {
	t.Parallel()

	for key, q := range questions {
		require.NotEmpty(t, q.text, key)
		require.NotEmpty(t, q.hint, "%s has no hint", key)
		require.NotContains(t, strings.ToLower(q.text+q.hint), "plugin", "%s calls a module a plugin", key)
	}
}

// TestAMultipleChoiceReadsNumbersSeparatedByCommas.
func TestAMultipleChoiceReadsNumbersSeparatedByCommas(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		typed []string
		want  []int
	}{
		"two numbers":              {[]string{"1,3"}, []int{1, 3}},
		"spaces around them":       {[]string{" 3 , 1 "}, []int{1, 3}},
		"one number twice":         {[]string{"3,1,3"}, []int{1, 3}},
		"an empty field, then one": {[]string{"1,,3", "2"}, []int{2}},
		"zero, then one":           {[]string{"0", "1"}, []int{1}},
		"Enter takes the default":  {[]string{""}, []int{1}},
		"the stream ends":          {nil, []int{1}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			require.Equal(t, c.want,
				answering(&out, c.typed...).chooseMany(questions["adds"], []string{"a", "b", "c"}, []int{1}))
		})
	}
}

// TestTheAddsMenuIsGroupedByScreen shows every screen's heading once, each
// slot's offer under it, and numbers that map back to the table.
func TestTheAddsMenuIsGroupedByScreen(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	a := answering(&out, "1")
	_ = slots(a, nil)

	for _, screen := range manifest.Screens() {
		require.Equal(t, 1, strings.Count(out.String(), screen.Doc()))
	}

	for _, slot := range manifest.Slots() {
		require.Contains(t, out.String(), slot.Offer)
	}
}

// TestAnActionAsksForItsIconAndWhetherItIsDestructive covers the one form
// with questions of its own. device-action is not a row of this worktree's
// table yet, so the row is built by hand rather than looked up, the way a
// slot added later would be asked about with no change here.
func TestAnActionAsksForItsIconAndWhetherItIsDestructive(t *testing.T) {
	t.Parallel()

	row := manifest.Slot{
		Name: "device-action", Form: manifest.FormAction,
		Offer: "an action on a printer's menu",
	}
	wrench := slices.Index(manifest.Icons(), manifest.Icon("wrench"))
	require.GreaterOrEqual(t, wrench, 0, "the icon list dropped wrench")

	var out bytes.Buffer

	a := answering(&out, "flush", "Flush", strconv.Itoa(wrench+2), "y")
	made := askContribution(a, row, scaffold.Contribution{}, "shelf", "Shelf")

	require.Equal(t, "flush", made.ID)
	require.Equal(t, "Flush", made.Title)
	require.Equal(t, manifest.Icon("wrench"), made.Icon)
	require.True(t, made.Destructive)
}

// TestAPastedAddressLosesItsSchemeAndSuffix. People paste what the browser
// shows, and the namespace comes from the host and the path alone.
func TestAPastedAddressLosesItsSchemeAndSuffix(t *testing.T) {
	t.Parallel()

	for typed, want := range map[string]string{
		"github.com/acme/shelf":             "github.com/acme/shelf",
		"https://github.com/acme/shelf.git": "github.com/acme/shelf",
		"http://gitlab.com/acme/shelf/":     "gitlab.com/acme/shelf",
		"  github.com/acme/shelf  ":         "github.com/acme/shelf",
	} {
		t.Run(typed, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, want, repoOf(typed))
		})
	}
}

// TestTheQuestionsWriteWhatTheSummaryShowed drives a whole run the way a
// person answers it, and reads the module it wrote.
func TestTheQuestionsWriteWhatTheSummaryShowed(t *testing.T) {
	t.Parallel()

	env := here(t)
	full := filepath.Join(env.workDir, "full")
	require.NoError(t, os.MkdirAll(full, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(full, "notes.txt"), nil, 0o600))

	var out bytes.Buffer

	a := answering(&out,
		"",                                  // the kind: Enter takes UI only
		"shelf",                             // the name
		"https://github.com/acme/shelf.git", // the address, pasted
		"1,11",                              // a page and a settings pane
		"full",                              // a folder that is not empty
		"",                                  // then the default
		"3",                                 // no git
		"n",                                 // no advanced step
		"1",                                 // write it
	)

	require.NoError(t, run(t.Context(), &out, a, env, nil))

	said := out.String()
	require.Contains(t, said, "\n? What kind of module is it?\n  A UI adds something")
	require.Contains(t, said, "  Its title will be: Shelf\n")
	require.Contains(t, said, "  Its namespace will be: com.github.aux.acme.shelf\n")
	require.Contains(t, said, full+" already holds something. Pick a new folder, or an empty one\n")
	require.Contains(t, said, "  kind       UI only\n")
	require.Contains(t, said, "  namespace  com.github.aux.acme.shelf  (the unique name its addresses start with)\n")
	require.Contains(t, said, "  adds       page shelf, settings shelf  (the contributes list in module.yaml)\n")

	// Nothing after the kind question speaks of Go to a UI-only author.
	afterKind := strings.Index(said, "What is its short name?")
	require.GreaterOrEqual(t, afterKind, 0)
	require.NotContains(t, said[afterKind:], "Go")

	dir := filepath.Join(env.workDir, "shelf")
	require.Equal(t, []string{"page:shelf", "settings:shelf"}, declared(t, dir))

	// The flag command printed at the end writes the same module with no
	// questions.
	line := said[strings.LastIndex(said, "go run ./cmd/new-module "):]
	words := strings.Fields(strings.SplitN(line, "\n", 2)[0])[3:]
	again := filepath.Join(t.TempDir(), "again")

	var quiet bytes.Buffer

	require.NoError(t, run(t.Context(), &quiet, nil, env, append(words, "-dir", again)))

	first, err := os.ReadFile(filepath.Join(dir, "module.yaml"))
	require.NoError(t, err)
	second, err := os.ReadFile(filepath.Join(again, "module.yaml"))
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
}

// TestEditingTheAnswersAsksAgainWithThemAsDefaults. A name changed on the
// second pass takes the folder with it, because nobody typed one.
func TestEditingTheAnswersAsksAgainWithThemAsDefaults(t *testing.T) {
	t.Parallel()

	env := here(t)

	var out bytes.Buffer

	a := answering(&out,
		"1", "shelf", "github.com/acme/shelf", "1", "", "3", "n",
		"2",                            // change the answers
		"", "rack", "", "", "", "", "", // Enter everywhere but the name
		"1", // write it
	)

	require.NoError(t, run(t.Context(), &out, a, env, nil))
	require.FileExists(t, filepath.Join(env.workDir, "rack", "module.yaml"))
	require.NoDirExists(t, filepath.Join(env.workDir, "shelf"))
}

// TestARenameOnTheSecondPassCarriesAcceptedDefaultsWithIt. An answer taken by
// pressing Enter is a default, not a choice: renaming the module on a later
// pass must carry the title, the repository address and a contribution's id
// and title with it, the way a rename already carries the folder.
func TestARenameOnTheSecondPassCarriesAcceptedDefaultsWithIt(t *testing.T) {
	t.Parallel()

	env := here(t)

	var out bytes.Buffer

	a := answering(&out,
		// pass 1: shelf, every default accepted, including in the advanced step
		// (title, then the page's id, title and icon)
		"1", "shelf", "", "", "", "3", "y", "", "", "", "",
		"2", // change the answers
		// pass 2: renamed to rack, every default accepted again, advanced skipped
		"", "rack", "", "", "", "", "n",
		"1", // write it
	)

	require.NoError(t, run(t.Context(), &out, a, env, nil))

	said := out.String()
	secondPass := said[strings.LastIndex(said, "\n? What kind of module is it?"):]

	require.Contains(t, secondPass, "Its title will be: Rack\n")
	require.NotContains(t, secondPass, "Shelf", "the title stayed fixed to the first pass's name")
	require.Contains(t, secondPass, "  name       rack, shown as Rack\n")
	require.Contains(t, secondPass,
		"  namespace  com.github.aux.example.rack  (the unique name its addresses start with)\n")
	require.Contains(t, secondPass, "  adds       page rack  (the contributes list in module.yaml)\n")
	require.NotContains(t, secondPass, "No flag carries",
		"nothing the person typed differs from the defaults for rack")

	require.FileExists(t, filepath.Join(env.workDir, "rack", "module.yaml"))
	require.NoDirExists(t, filepath.Join(env.workDir, "shelf"))
}

// TestCancellingWritesNothing.
func TestCancellingWritesNothing(t *testing.T) {
	t.Parallel()

	env := here(t)

	var out bytes.Buffer

	a := answering(&out, "1", "shelf", "github.com/acme/shelf", "1", "", "3", "n", "3")

	require.NoError(t, run(t.Context(), &out, a, env, nil))
	require.Contains(t, out.String(), "Cancelled. Nothing was written.")
	require.NoDirExists(t, filepath.Join(env.workDir, "shelf"))
}

// TestAStreamThatEndsCancelsAtTheSummary. Ctrl+D, or a stream that is
// /dev/null, answers every question after it with its default, and the write
// menu takes that as a cancel rather than its own default "yes, write it": a
// run nobody is watching must write nothing.
func TestAStreamThatEndsCancelsAtTheSummary(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	a := answering(&out, "3", "shelf")
	dir := filepath.Join(t.TempDir(), "shelf")

	require.NoError(t, run(t.Context(), &out, a, here(t),
		[]string{"-checkout", theCheckout(t), "-dir", dir, "-git=false"}))
	require.Contains(t, out.String(), "Cancelled. Nothing was written.")
	require.NoDirExists(t, dir)
}

// TestDevNullIsNotATerminal. /dev/null is a character device like a real
// terminal, so `isTerminal` also checks it by name: a process given
// /dev/null on stdin, the way `nohup` and a closed-stdin CI runner do, is
// never somebody to ask.
func TestDevNullIsNotATerminal(t *testing.T) {
	t.Parallel()

	devNull, err := os.Open(os.DevNull)
	require.NoError(t, err)

	t.Cleanup(func() { _ = devNull.Close() })

	require.False(t, isTerminal(devNull))
}

// TestWithStdinFromDevNullTheMissingFlagsAreNamed. A run whose stdin is
// /dev/null takes the flags-only path, the same as a pipe or a closed
// terminal, so a required flag it does not carry fails rather than being
// asked.
func TestWithStdinFromDevNullTheMissingFlagsAreNamed(t *testing.T) {
	t.Parallel()

	devNull, err := os.Open(os.DevNull)
	require.NoError(t, err)

	t.Cleanup(func() { _ = devNull.Close() })

	var out bytes.Buffer

	err = start(t.Context(), &out, devNull, []string{"-name", "x", "-repo", "y"})
	require.ErrorIs(t, err, errUsage)
	require.Contains(t, err.Error(), "-kind")
}

// TestTheToolsAreCheckedRightAfterTheKind. Nothing else is asked of a person
// whose machine cannot build what they chose.
func TestTheToolsAreCheckedRightAfterTheKind(t *testing.T) {
	t.Parallel()

	env := environment{workDir: t.TempDir(), version: versions(map[string]string{})}

	var out bytes.Buffer

	err := run(t.Context(), &out, answering(&out, "2", "shelf"), env, nil)
	require.ErrorIs(t, err, errTools)
	require.Contains(t, err.Error(), "go is not installed")
	require.NotContains(t, out.String(), "What is its short name?")
}

// TestAFlagIsNeverAskedAbout. The command line is the answer, and a make
// target that passes one must not be asked to confirm it.
func TestAFlagIsNeverAskedAbout(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	given := map[string]bool{
		"name": true, "repo": true, "kind": true, "dir": true, "contributes": true, "git": true, "remote": true,
	}

	held := interview(t.Context(), answering(&out, "n"), given, answers{
		name: "shelf", repo: "github.com/acme/shelf", shape: shapeUI, workDir: t.TempDir(),
		//nolint:exhaustive // one chosen slot is the whole point of this fixture
		contributions: map[manifest.SlotName]scaffold.Contribution{manifest.SlotDeviceCard: {}},
	})

	require.Equal(t, "shelf", held.name)
	require.NotContains(t, out.String(), questions["repo"].text)
	require.NotContains(t, out.String(), questions["adds"].text)
	//nolint:exhaustive // one chosen slot is the whole point of this fixture
	require.Equal(t, map[manifest.SlotName]scaffold.Contribution{manifest.SlotDeviceCard: {}}, held.contributions)
}

// TestOutsideACheckoutNothingIsOfferedToRun. There is no install.sh to run,
// so the person is told why and asked nothing.
func TestOutsideACheckoutNothingIsOfferedToRun(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	env, seen := faking(here(t), "", nil)
	require.NoError(t, startDevLoop(t.Context(), answering(&out, "y"), &out, env,
		answers{workDir: env.workDir}, scaffold.Plan{Dir: t.TempDir(), UI: true}, &stepper{out: &out}))
	require.Empty(t, seen.installs)
	require.NotContains(t, out.String(), "?")
	require.Contains(t, out.String(), "from npm, and it is not published yet")
	require.NotContains(t, out.String(), "Go", "a UI-only author was told about Go")
}

// TestTheQuestionsAreStyledOnlyWithColor. On a terminal the question, its hint
// and the prompt look different from the answer. Without color the markers
// stay, so a pasted transcript still tells them apart.
func TestTheQuestionsAreStyledOnlyWithColor(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		color bool
		want  []string
	}{
		"with color": {true, []string{
			"\n\x1b[1m? Which?\x1b[0m\n\x1b[2m  One of them.\x1b[0m\n  1) a\n",
			"\x1b[36m›\x1b[0m \x1b[2m[3]\x1b[0m ",
			"\x1b[32m✔\x1b[0m b\n",
		}},
		"plain": {false, []string{"\n? Which?\n  One of them.\n  1) a\n", "› [3] ", "✔ b\n"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			a := answering(&out, "2")
			a.color = c.color

			require.Equal(t, 2, a.choose(question{"Which?", "One of them."}, []string{"a", "b: the second", "c"}, 3))

			for _, want := range c.want {
				require.Contains(t, out.String(), want)
			}

			if !c.color {
				require.NotContains(t, out.String(), "\x1b")
			}
		})
	}
}

// TestANameWithBrokenBytesIsShownAsItReads. A terminal that erases half of a
// character leaves bytes that are not text, and the message shows them as the
// replacement character rather than as escapes.
func TestANameWithBrokenBytesIsShownAsItReads(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	a := answering(&out, "е\xd1test-module", "shelf")
	require.Equal(t, "shelf", a.label(questions["name"], ""))
	require.Contains(t, out.String(), "\"е�test-module\" is not a short name")
	require.NotContains(t, out.String(), `\x`)

	_, err := planFor(answers{name: "е\xd1test-module", shape: shapeProcess, workDir: t.TempDir()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "\"е�test-module\" cannot name an instance")
	require.NotContains(t, err.Error(), `\x`)
}

// gitHub is a gh logged in as login. It answers repo create with failure, and
// records the words of every call in calls.
func gitHub(login string, failure error, calls *[]string) func(context.Context, string, ...string) (string, error) {
	return func(_ context.Context, _ string, args ...string) (string, error) {
		*calls = append(*calls, strings.Join(args, " "))

		switch args[0] {
		case "auth":
			return "", nil
		case "api":
			return login, nil
		default:
			return "", failure
		}
	}
}

// TestWithGhLoggedInANewGitHubRepositoryIsTheDefault. The person just presses
// Enter, and the module lands in a new private repository of their own.
func TestWithGhLoggedInANewGitHubRepositoryIsTheDefault(t *testing.T) {
	isolateGit(t)

	var calls []string

	env := here(t)
	env.gh = gitHub("acme", nil, &calls)

	var out bytes.Buffer

	// the kind, the name, the address, the contributions, the folder and the
	// git question all take Enter, then no advanced step, then write it
	a := answering(&out, "", "shelf", "", "", "", "", "n", "1")
	require.NoError(t, run(t.Context(), &out, a, env, nil))

	said := out.String()
	require.Contains(t, said, "› [github.com/acme/shelf] ")
	require.Contains(t, said, "\n? Where should its code live?\n")
	require.Contains(t, said, "  1) a new private repository on GitHub, acme/shelf: ")
	require.Contains(t, said, "  4) no git: ")
	require.Contains(t, said, "  git        a first commit, pushed to a new private repository on GitHub, acme/shelf\n")
	require.Contains(t, said, "made a private repository on GitHub, acme/shelf, and pushed the first commit to it")
	require.Contains(t, said, " -repo github.com/acme/shelf -contributes page -dir "+shelfIn()+" -github\n")

	require.Equal(t, []string{
		"auth status", "api user --jq .login", "repo create acme/shelf --private --source . --remote origin --push",
	}, calls, "gh is asked who is logged in once, before the questions")
}

// TestWithoutGhALocalRepositoryIsTheDefault. Nothing is offered that cannot
// work, and the menu keeps its numbers in order.
func TestWithoutGhALocalRepositoryIsTheDefault(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	a := answering(&out, "", "shelf", "", "", "", "", "n", "3")
	require.NoError(t, run(t.Context(), &out, a, here(t), nil))

	said := out.String()
	require.Contains(t, said, "› [github.com/example/shelf] ")
	require.NotContains(t, said, "GitHub")
	require.Contains(t, said, "  1) a repository I already made (paste its address): ")
	require.Contains(t, said, "  3) no git: ")
	require.Contains(t, said, "  git        a first commit, only here\n")
}

// TestARemoteThatIsNotEmptyIsAskedForAgainBeforeAnythingIsWritten. A remote
// with commits refuses the push, so it is refused while the person can still
// pick another, and so is one that cannot be reached.
func TestARemoteThatIsNotEmptyIsAskedForAgainBeforeAnythingIsWritten(t *testing.T) {
	isolateGit(t)

	full := t.TempDir()
	gitIn(t, full, "init", "--quiet")
	gitIn(t, full, "commit", "--quiet", "--allow-empty", "--message", "mine")

	empty := filepath.Join(t.TempDir(), "empty.git")
	gitIn(t, "", "init", "--quiet", "--bare", empty)

	missing := filepath.Join(t.TempDir(), "nothing-here.git")
	env := here(t)

	var out bytes.Buffer

	a := answering(&out, "", "shelf", "", "", "",
		"1", // a repository I already made
		full, missing, empty,
		"n", // no advanced step
		"3", // cancel
	)
	require.NoError(t, run(t.Context(), &out, a, env, nil))

	said := out.String()
	require.Contains(t, said, "  "+full+" already has commits. Pick an empty repository.\n")
	require.Contains(t, said, "  "+missing+" cannot be reached: ")
	require.Contains(t, said, "  git        a first commit, pushed to "+empty+"\n")
	require.NoDirExists(t, filepath.Join(env.workDir, "shelf"))
}

// TestInterviewAsksMemoryForAProcess. A process is asked what it needs, a
// figure outside the manifest's bounds is asked again once and then dropped,
// and a UI-only module is never asked.
func TestInterviewAsksMemoryForAProcess(t *testing.T) {
	t.Parallel()

	given := map[string]bool{
		"name": true, "repo": true, "kind": true, "dir": true, "contributes": true, "git": true,
	}
	base := answers{name: "shelf", repo: "github.com/acme/shelf", workDir: t.TempDir()}

	for name, c := range map[string]struct {
		typed []string
		want  int
	}{
		"a figure":            {[]string{"512", "n"}, 512},
		"empty keeps default": {[]string{"", "n"}, 0},
		"asked again once":    {[]string{"9000", "512", "n"}, 512},
		"then falls back":     {[]string{"9000", "many", "n"}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			request := base
			request.shape = shapeProcess

			held := interview(t.Context(), answering(&out, c.typed...), given, request)
			require.Equal(t, c.want, held.memoryMiB)
			require.Contains(t, out.String(), questions["memory"].text)
		})
	}

	var out bytes.Buffer

	request := base
	request.shape = shapeUI
	//nolint:exhaustive // one chosen slot is the whole point of this fixture
	request.contributions = map[manifest.SlotName]scaffold.Contribution{manifest.SlotPage: {}}

	held := interview(t.Context(), answering(&out, "512", "n"), given, request)
	require.Zero(t, held.memoryMiB)
	require.NotContains(t, out.String(), questions["memory"].text)
}
