package ui

import (
	"math/rand/v2"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/joshw/zephyrlily/internal/cmdarg"
)

// Feature tips: one is shown per session, chosen at random, and all of them are
// browsable with '%help tips'.
//
// zlily has more features than anything surfaces. %help only answers a question
// the user already knew to ask, and a good deal of what is worth knowing is
// reachable only by keybinding (M-s, C-r, M-x, M-m) or has no help topic in
// either registry. A tip per login is the cheapest way for someone who never
// reads docs to still meet the product.
//
// Most tips exist to point at a topic that is already written, and should stay
// short enough to be worth reading in passing. Where a tip describes something
// with no %help topic to defer to - the browser client, paste mode, name
// completion - its long text has to stand on its own instead.
//
// Adding one is a single entry in the slice below. Keep short to about three
// lines: the whole notice is appended as one output item, and syncViewportContent
// pauses at -- MORE -- when a single append exceeds a viewport height.

// tip is one entry in the pool. long is what '%help tips <name>' prints; short
// is what lands in the scrollback once a session.
type tip struct {
	name    string   // '%help tips <name>'; lowercase, no spaces
	heading string   // one line, shown in the listing and above the long text
	short   []string // pre-wrapped; what prints in-session
	long    []string // the fuller version
}

// tips is the pool, in a deliberate order: the listing prints it as-is, so
// related tips sit together. A slice rather than a map so that order is
// something we chose rather than whatever the runtime felt like.
var tips = []tip{
	{
		name:    "share",
		heading: "One session, many clients",
		short: []string{
			"Your session belongs to a proxy rather than to the client you are",
			"typing in, so several zlilys can share one. Plain 'zlily' keeps its",
			"proxy to itself; 'zlily server' is the one others can attach to.",
		},
		long: []string{
			"zlily talks to Lily through a proxy, and the session belongs to the",
			"proxy rather than to any one client. The proxy holds the Lily",
			"connection, the list of who and what is on the server, and the",
			"conversation so far - so a client can come and go without the session",
			"noticing, and you get your scrollback back when you return.",
			"",
			"Which proxy matters, and the default is the private one. Plain 'zlily'",
			"runs a proxy inside itself, listening only on this machine on a port",
			"it picks, and it ends when you quit - nothing else can attach to it.",
			"To share a session you need a proxy that outlives the client:",
			"",
			"  zlily server --lily rpi.lily.org:7777    somewhere that stays up",
			"  zlily client --proxy https://lily.example.org",
			"",
			"Then shutting a laptop mid-conversation and picking it up elsewhere",
			"works, because the session was never in the laptop.",
			"",
			"Every attached client sees the same conversation, including the lines",
			"you send: the proxy echoes them back to everyone rather than having",
			"each client draw its own, so all of them show the message that Lily's",
			"'(message sent to ...)' is acknowledging, in the same place.",
			"",
			"Give --proxy a URL with a scheme when the proxy is behind TLS. A bare",
			"host:port is spoken over plain HTTP, so '--proxy lily.example.org:443'",
			"sends cleartext at the TLS port and the reverse proxy answers 404",
			"without zlily ever hearing about it.",
			"",
			"Some Lily communities run a shared proxy you can point a client at.",
			"Running your own is a single command - see '%help tips web'.",
		},
	},
	{
		name:    "web",
		heading: "zlily in a browser tab",
		short: []string{
			"This same TUI compiles to WebAssembly and runs in a browser, keys and",
			"all. 'zlily server --web' serves it locally; 'zlily deploy' stands up",
			"a real one with TLS. A page reload rejoins the session it left.",
		},
		long: []string{
			"The browser client is not a separate UI - it is this program compiled",
			"to WebAssembly with xterm.js as the terminal, so the keybindings, the",
			"pager and the commands are the ones you already know.",
			"",
			"A released zlily already carries the browser build inside it, so",
			"serving it is one command:",
			"",
			"  zlily server --web --lily rpi.lily.org:7777",
			"",
			"then open http://localhost:7888/. (Building from source, the .wasm is",
			"not in the repository and has to be made first: GOOS=js GOARCH=wasm go",
			"build -o internal/webstatic/term/zlily.wasm ./cmd/zlily-wasm.)",
			"",
			"On the internet, from the Linux host that will run it:",
			"",
			"  zlily deploy --domain lily.example.org --email you@example.org",
			"",
			"That writes a ./zlily-deploy/ directory - Dockerfile, compose file,",
			"Traefik with Let's Encrypt - and brings it up. The image ships the",
			"zlily binary you ran it with, so there is no toolchain to install and",
			"nothing to download. Back up zlily-deploy/letsencrypt/acme.json.",
			"",
			"A reload re-attaches rather than asking for a password again: the page",
			"keeps the proxy session token and resumes the live session with it.",
			"Some things a browser cannot give up - C-z, and whichever of C-n/C-t",
			"the browser keeps for itself - and a saved password is not offered,",
			"since a page has nowhere safe to keep one.",
		},
	},
	{
		name:    "schedule",
		heading: "Make zlily do things later",
		short: []string{
			"'%after 5m bob;back' sends that in five minutes; '%every 1h' repeats.",
			"They run on the proxy, so they keep firing while no client is even",
			"attached. Bare '%cron' lists what is pending.",
		},
		long: []string{
			"  %after <interval> <command>    once, after the interval",
			"  %every <interval> <command>    repeatedly",
			"  %cron after|every <interval> <command>",
			"  %cron cancel <id>",
			"  %cron                          list what is scheduled",
			"",
			"Intervals are N or Ns seconds, Nm minutes, Nh hours, Nd days, and are",
			"case-insensitive - see '%help interval'. A command may contain \\n to",
			"run several in sequence.",
			"",
			"The command can be a send, which is what 'bob;back' is - a private",
			"message to bob. See '%help tips expand' if that shape is new.",
			"",
			"The jobs live on the proxy, which has two consequences worth knowing:",
			"they keep running when every client has detached, and when one fires",
			"it announces itself to all of them. They are cancelled when the",
			"session ends, so '%cron' in a zlilyStartup memo is how a standing job",
			"comes back - see '%help tips startup'.",
			"",
			"See '%help cron'.",
		},
	},
	{
		name:    "on",
		heading: "React to what people say",
		short: []string{
			"%on fires a command when something matches:",
			"  %on public like \"ping (.*)\" \"$sender;pong $1\"",
			"Filter by from, to, like, random or once; '%on list' shows them.",
		},
		long: []string{
			"  %on <event> [filters...] <action>",
			"  %on list",
			"  %on clear <id>",
			"",
			"Events worth using are public, private and emote.",
			"",
			"Filters:",
			"  from <user>      only from this sender",
			"  to <dest>        only to this destination",
			"  value <string>   exact text",
			"  like <regexp>    case-insensitive match",
			"  random <N>       roughly one time in N",
			"  once <interval>  at most once per interval",
			"",
			"In the action, $sender, $target and $value stand for the event, and",
			"$1..$9 for the capture groups of a 'like' pattern.",
			"",
			"  %on emote to bar like \"yawn\" once 5m \"bar;yawns.\"",
			"",
			"The action is a line zlily types for you, so 'bar;yawns.' is a send to",
			"bar - see '%help tips expand' for how sends are written.",
			"",
			"Two things that surprise people. Events you send yourself are ignored",
			"unless a 'from' filter names you, so a trigger cannot answer itself.",
			"And %on is the one command with quote-aware parsing, which is why a",
			"pattern containing spaces has to be quoted.",
			"",
			"Like scheduled jobs, triggers run on the proxy and last as long as the",
			"session. See '%help on'.",
		},
	},
	{
		name:    "shorten",
		heading: "Shorten a URL in place",
		short: []string{
			"M-s (that is Alt-s) replaces the first URL before the cursor with a",
			"short one and keeps the site in brackets after it: a bare short link",
			"tells a reader nothing. Nothing is shortened without you asking.",
		},
		long: []string{
			"Press M-s - Alt-s - with a URL in the line you are composing:",
			"",
			"  https://s.u13.net/7f2 [arstechnica.com]",
			"",
			"The bracketed host is the point of it. The substitution happens in the",
			"input line, so it is ordinary editable text before you send it, and if",
			"the shortener fails the error prints and your URL is left alone.",
			"",
			"'%shorten' reports the service in use and '%shorten <name>' changes it.",
			"See '%help shorten' for the services and how the API key is found.",
		},
	},
	{
		name:    "history",
		heading: "Find that line you typed",
		short: []string{
			"C-r (Ctrl-r) searches back through what you have typed and C-s",
			"forward, matching as you go and highlighting the hit in place. Enter",
			"accepts it; C-g or Esc puts back the line you started from.",
		},
		long: []string{
			"C-r (Ctrl-r) searches backward, C-s forward. Matching is",
			"case-insensitive and incremental: the match is highlighted where it",
			"sits and the cursor is left at the start of it. Pressing C-r again",
			"steps to the next match, skipping the one you are on.",
			"",
			"The line you are currently typing is part of the search space, so C-r",
			"can find text in a line you have not sent yet.",
			"",
			"Any cursor motion - C-a, C-e, C-b, C-f, M-b, M-f - ends the search,",
			"keeps the match and then does what the key normally does, so you can",
			"search and go straight to editing. Enter accepts, C-g or Esc restores",
			"what you had before searching.",
			"",
			"C-p and C-n (or the arrow keys) step through history without searching.",
			"See '%help keys'.",
		},
	},
	{
		name:    "mouse",
		heading: "Wheel scrolling, click to position",
		short: []string{
			"'%mouse on' - or M-m, which is Alt-m - makes the wheel scroll the",
			"output and a click in the input line move the cursor there. Off by",
			"default: it costs your terminal's click-drag selection. [M] means on.",
		},
		long: []string{
			"'%mouse on' or M-m (Alt-m) turns it on, and the status bar carries [M]",
			"while it is - deliberately, so that 'why has selection stopped",
			"working' is something you can see rather than have to remember.",
			"",
			"That is the trade: a terminal can report mouse events to the program",
			"or do its own click-drag selection, not both. Most terminals offer a",
			"bypass modifier - Shift in xterm, GNOME Terminal and Windows Terminal,",
			"Option in iTerm2, Fn or Shift in Terminal.app - and otherwise M-m off,",
			"drag, M-m on works fine. That pairing is why the key binding exists",
			"next to the command.",
			"",
			"'%mouse on' in a zlilyStartup memo turns it on at every login. In the",
			"browser the selection is the browser's, so none of the modifier",
			"guidance applies. See '%help mouse'.",
		},
	},
	{
		name:    "style",
		heading: "Recolor the parts you read most",
		short: []string{
			"'%style public-sender fg brightcyan' recolors who said it; there are",
			"25 named pieces of the display, each with a foreground, background and",
			"bold. '%style' on its own lists them and what they are set to.",
		},
		long: []string{
			"  %style                       list every element and its setting",
			"  %style <name>                show one",
			"  %style <name> fg|bg <color>  set a color",
			"  %style <name> bold on|off",
			"  %style <name> default        back to the built-in look",
			"  %style all default",
			"",
			"Colors can be a number from 0 to 255, a name like brightyellow, or",
			"#rrggbb.",
			"",
			"The names cover senders, bodies, blurbs and recipients for public and",
			"private messages separately, plus emotes, command output, errors, the",
			"prompt, the input line, the status bar, log lines, misspellings, the",
			"cursor and link previews.",
			"",
			"Changes last for the session. Put the ones you want to keep in a",
			"zlilyStartup memo - see '%help tips startup'. See '%help style'.",
		},
	},
	{
		name:    "linkpreview",
		heading: "See where a link goes before you send it",
		short: []string{
			"Type or paste a URL and zlily shows in gray what the page says about",
			"itself. The gray is not part of your message: Tab turns it into real",
			"text, Enter just drops it. '%linkpreview off' stops the fetching.",
		},
		long: []string{
			"Previews are on by default. The summary comes from the page's own",
			"metadata and is drawn after the URL in gray.",
			"",
			"It is never silently part of what you send. Tab accepts every preview",
			"on the line and turns it into ordinary text; plain Enter sends the",
			"line without them. Backspace at the end of a previewed URL removes",
			"that one preview, and a second Backspace then edits the URL itself.",
			"A URL whose page offers nothing shows '(no preview available)', which",
			"Tab steps over rather than accepting.",
			"",
			"A preview follows a short link to its destination, so you get a",
			"description of the thing rather than of the shortener.",
			"",
			"Nothing you type goes to a language model - but the page is fetched,",
			"which tells its host that someone is interested in that URL. Turn",
			"previews off before pasting an internal address or a single-use link.",
			"See '%help linkpreview'.",
		},
	},
	{
		name:    "startup",
		heading: "Run commands every time you connect",
		short: []string{
			"'%memo edit zlilyStartup' opens a memo whose lines run on every login",
			"- Lily's own '/' commands, sends, and zlily's '%' commands alike. It",
			"is how %style, %spell, %mouse and your aliases stop being session-only.",
		},
		long: []string{
			"Put one command per line in a memo called zlilyStartup:",
			"",
			"  %memo edit zlilyStartup",
			"",
			"Two kinds of command can go in it, and the prefix says which is which:",
			"'/' is a Lily command and goes to the server (/who, /finger, /join),",
			"while '%' is a zlily command and is handled here or by the proxy",
			"(%style, %alias, %mouse). Plain sends work too - see",
			"'%help tips expand' for how those are written.",
			"",
			"Blank lines and # comments are skipped. Everything else runs on each",
			"connect, including after a reconnect, since a reconnect is a fresh",
			"Lily session. '%startup' re-runs it without reconnecting.",
			"",
			"It is replayed by the proxy rather than by any one client, so it works",
			"the same whichever client you attach with, and a client that attaches",
			"after login still receives the client-side commands it contained.",
			"",
			"Most of zlily's adjustable settings are session-only by design, and",
			"this is the thing that makes them stick: %style, %spell, %mouse on,",
			"%alias, standing %cron jobs and %on triggers. %echo is handy here for",
			"labelling what is happening. See '%help startup'.",
		},
	},
	{
		name:    "alias",
		heading: "Teach zlily a new command",
		short: []string{
			"'%alias inbeener /who beener $*' makes %inbeener. $1..$9 and $* take",
			"arguments, \\n separates several commands. Expansion happens on the",
			"proxy, so every client attached to the session gets it.",
		},
		long: []string{
			"  %alias <name> <commands>",
			"  %alias list [<name>]",
			"  %alias clear <name>",
			"  %alias                    list all of them",
			"",
			"In the commands, $1..$9 are the arguments, $0 the alias name and $* all",
			"the arguments; \\n separates commands.",
			"",
			"  %alias hi bob;hi there\\njim;hello",
			"",
			"That one makes '%hi' greet two people, because each half is a send -",
			"see '%help tips expand' for how those are written.",
			"",
			"Names are letters, digits and underscore, and are case-insensitive, so",
			"%greet and %GREET are one alias. %alias itself is never expanded, so",
			"alias management always works however badly you have aliased things.",
			"",
			"Aliases are expanded on the proxy before dispatch, which is why every",
			"client gets them for free - and why they last only as long as the",
			"session. Keep the ones you want in a zlilyStartup memo. See",
			"'%help alias'.",
		},
	},
	{
		name:    "expand",
		heading: "Type two letters, not a whole pseudo",
		short: []string{
			"You send in Lily by writing 'pseudo;your message'. Type part of the",
			"pseudo and press ';' and zlily finishes it: 'bee;' becomes 'beener;',",
			"and you carry on typing the message. ':' does the same for an emote.",
		},
		long: []string{
			"A pseudo is the name a Lily user is seen and addressed as - not the",
			"username they logged in with (see '%help tips password'). A send is a",
			"destination, a separator and your text:",
			"",
			"  beener;are you there?    a private message to beener",
			"  beener:waves             an emote, which is public",
			"  -meeting;starting now    a send to a discussion (hence the '-')",
			"",
			"You rarely type the whole destination. With part of a pseudo or",
			"discussion name before the cursor:",
			"",
			"  ;   finish it and start a private send",
			"  :   finish it and start an emote",
			"  ,   finish it and keep adding recipients",
			"  =   recall the last set of recipients you sent to together",
			"",
			"So 'bee;' becomes 'beener;'. Spaces in a name become underscores, and",
			"a discussion is completed with the leading '-' that Lily's syntax",
			"wants, so you do not have to remember it. An ambiguous fragment",
			"expands to nothing rather than guessing, and several matches open a",
			"list to pick from (Esc dismisses it).",
			"",
			"At the very start of an empty line the same keys recall rather than",
			"complete: ':' inserts the last person who paged you privately, ';'",
			"your last destination, '=' the last set of recipients. Tab at the",
			"start of a line does the same as ';', and Tab after a partial",
			"destination cycles your five most recent destinations.",
			"",
			"Tab also completes a name argument to the Lily commands that take one",
			"- /who, /finger, /join, /ignore and friends.",
			"",
			"Expansion keeps out of the way of anything that is not a send: it does",
			"nothing when the text before the cursor already has a ':', ';' or '/'",
			"in it, or when the line starts with '$', '?' or '%'. And while a link",
			"preview is showing, Tab accepts the preview instead of completing.",
		},
	},
	{
		name:    "keys",
		heading: "Familiar editing keys",
		short: []string{
			"Keys are written C-a and M-f, meaning Ctrl-a and Alt-f. The editing",
			"keys you may know from emacs or readline all work; '%help keys' is",
			"the full reference. If your terminal keeps Alt, press Esc first.",
		},
		long: []string{
			"The notation first, since it is used everywhere: C- is Ctrl and M- is",
			"Alt, so C-a is Ctrl-a and M-f is Alt-f. Because a good many terminals",
			"keep Alt for their own menus, Esc also works as the M- prefix - press",
			"and release Esc, then the key. Esc f is M-f, and Esc g is M-g.",
			"",
			"The bindings are the ones you may already know from emacs or readline,",
			"so the keys that work at a shell prompt work here too.",
			"",
			"'%help keys' prints the whole reference. The parts people are pleased",
			"to discover:",
			"",
			"Consecutive kills accumulate rather than replace: C-k, C-w, C-u and",
			"M-d in a row build up one kill buffer that C-y then yanks back whole.",
			"",
			"M-t transposes words, M-c capitalizes, M-u upcases and M-l downcases",
			"the word ahead of the cursor.",
			"",
			"Enter on an empty line while you are scrolled back pages down instead",
			"of sending an empty line.",
			"",
			"M-< and M-> jump to the top and bottom of the scrollback; M-, and M-.",
			"move it a line at a time.",
		},
	},
	{
		name:    "spell",
		heading: "Typos are underlined as you type",
		short: []string{
			"zlily spell-checks the input line against a built-in dictionary and",
			"marks what it does not know. '%spell allow <word>' teaches it one,",
			"'%spell forbid <word>' makes it flag one it would have accepted.",
		},
		long: []string{
			"  %spell            list the words you have added",
			"  %spell on|off",
			"  %spell allow <word>...    accept these",
			"  %spell forbid <word>...   flag these",
			"  %spell remove <word>...   forget what you said about these",
			"  %spell reset",
			"",
			"Forbid beats allow beats the dictionary. Matching is case-insensitive,",
			"so allowing a word also allows it at the start of a sentence.",
			"",
			"Your word lists last for the session; keep them in a zlilyStartup",
			"memo. See '%help spell'.",
		},
	},
	{
		name:    "password",
		heading: "Stop typing your password",
		short: []string{
			"Tick 'Remember password' in the login box, or run '%save-password'.",
			"It goes in your OS keyring when there is one, and otherwise in a",
			"0600 file. Your username is remembered either way.",
		},
		long: []string{
			"  %save-password            remember the one you just logged in with",
			"  %forget-password [user]",
			"",
			"In the login dialog, Tab moves between fields and Space toggles the",
			"'Remember password' box; ticking it does the same as %save-password,",
			"and clearing it on a later login removes what was stored.",
			"",
			"Your username is remembered whether or not you save a password, which",
			"is why the dialog opens with the cursor already in the password field.",
			"",
			"Your username is not your pseudo. You log in with a username and",
			"password, and then choose a pseudo - the name other Lily users see",
			"you as, and the one they send to. Saved passwords are keyed by server",
			"and username, so changing pseudo leaves them alone.",
			"",
			"Two stores, read in the opposite order they are written to. Saving",
			"prefers the OS keyring (macOS Keychain, Windows Credential Manager,",
			"freedesktop Secret Service) so that nothing lands on disk in the",
			"clear. Reading checks ~/.config/zlily/credentials first, so a line you",
			"added by hand is a visible override rather than something the keyring",
			"silently wins over. On a headless host the file is usually all there",
			"is; zlily ignores it entirely if anyone else can read it, so chmod 600.",
			"",
			"The password is kept as plaintext because SLCP authenticates in",
			"plaintext on every connect: there is no token to keep instead and",
			"nothing useful to hash. It is protected by file permissions, not by",
			"encryption. One Lily rejects is deleted rather than offered again.",
			"",
			"See '%help password'.",
		},
	},
	{
		name:    "snapshot",
		heading: "When the display goes wrong, press M-x",
		short: []string{
			"M-x (Alt-x) writes a diagnostic snapshot without disturbing what you",
			"were typing. It measures the terminal before repainting, which is the",
			"half of a display bug that is otherwise gone by the time anyone looks.",
		},
		long: []string{
			"M-x (Alt-x), or '%debug snapshot [path]', writes a file called",
			"~/zlily-debug-<time>.txt: terminal geometry, the input line's state,",
			"recent keystrokes, proxy traffic, responsiveness metrics, the rendered",
			"frame, and the raw bytes recently sent to the terminal.",
			"",
			"It also asks the terminal about itself, which is the part that makes a",
			"display bug diagnosable: the size the kernel reports (flagged when it",
			"disagrees with what zlily believes), where the terminal says the",
			"cursor is, and under GNU screen, screen's own dump of the display.",
			"Those are taken before the snapshot repaints - and a repaint is",
			"exactly what clears this kind of corruption. So if the display is",
			"wrong, take the snapshot before doing anything to fix it.",
			"",
			"M-x exists as a key and not only a command because typing a command",
			"means clearing your input line, and the input line's contents and",
			"height are part of what a display bug needs recorded.",
			"",
			"The file holds recent typed input and screen content - read it before",
			"sharing it. See '%help snapshot'.",
		},
	},
	{
		name:    "page",
		heading: "Output waits for you",
		short: []string{
			"More than a screenful pauses rather than scrolling past: the status",
			"bar shows -- MORE (n) -- with how many lines are waiting, and Enter",
			"advances. '%page off' lets it stream.",
		},
		long: []string{
			"The pager is on by default. When more arrives than fits, zlily stops",
			"and the status bar reads -- MORE (n) --, n being the lines you have",
			"not seen; Enter advances a page. It re-arms once you have caught up,",
			"so output arriving while you are away pauses too instead of scrolling",
			"past unread.",
			"",
			"Sending a line only jumps you to the bottom if you were already there.",
			"Read back through the scrollback and type, and the view stays where",
			"you were reading.",
			"",
			"A reconnect brings you back to where you had read to rather than to",
			"the end of the stream - see '%help tips reconnect'.",
			"",
			"'%page' shows the setting, '%page off' disables it. See '%help page'.",
		},
	},
	{
		name:    "editor",
		heading: "Edit your info without leaving zlily",
		short: []string{
			"'%info edit' opens your info - the text /finger shows - in an editor",
			"inside zlily, filled in with what is there now; C-s (Ctrl-s) saves and",
			"Esc cancels. '%memo edit <name>' does the same for a memo.",
		},
		long: []string{
			"  %info edit [target]",
			"  %memo edit [target] <name>",
			"",
			"Both open an in-TUI editor pre-populated with the current text, so",
			"editing is editing rather than retyping. C-s saves, Esc cancels. With",
			"no target they act on your own.",
			"",
			"Typing Lily's own '/info set' or '/memo set' is intercepted and points",
			"you here, because those replace the text with whatever you typed on",
			"the one line rather than letting you change part of it.",
			"",
			"'%memo edit zlilyStartup' is how you edit the memo that runs on every",
			"login - see '%help tips startup'.",
		},
	},
	{
		name:    "perf",
		heading: "Is it actually getting slower?",
		short: []string{
			"'%debug perf' prints how responsive this session has been, as a trend",
			"table. Read it oldest row to newest: if the p95 columns climb, the",
			"slowdown is real, and the gauges beside them say what grew with it.",
		},
		long: []string{
			"  %debug perf",
			"",
			"Per-operation latency - typing, scrolling, incoming events, repaints -",
			"for the whole session, then one row per time window with p95 per",
			"operation alongside heap size, goroutine count, scrollback size and",
			"bytes written to the terminal.",
			"",
			"The comparison is the point. A single row says little; oldest against",
			"newest says whether 'it feels sluggish' is real, and the gauges in the",
			"same row say what was growing while it happened.",
			"",
			"Set ZLILY_PERF_WINDOW (e.g. 10s) before starting zlily for a finer",
			"trend than the default one-minute windows. The same table goes into",
			"'%debug snapshot'. See '%help perf'.",
		},
	},
	{
		name:    "ascify",
		heading: "Paste from the web without mangling it",
		short: []string{
			"Lily is ASCII-only, so zlily folds everything on the way out: curly",
			"quotes straighten, an em dash becomes --, a euro sign becomes EUR, and",
			"an emoji smiley becomes the emoticon it was drawn from.",
		},
		long: []string{
			"You can paste a sentence off a web page without thinking about it.",
			"Outbound text is folded to ASCII, and the interesting cases are",
			"hand-picked so that the ASCII stands in for the meaning rather than",
			"for the shape:",
			"",
			"  ’ “ ”   become straight quotes",
			"  – —     become - and --",
			"  …       becomes ...",
			"  € £ ¥   become EUR, GBP, JPY",
			"  →       becomes ->",
			"  a smiley becomes the emoticon it was drawn from",
			"",
			"Everything else falls through cheaper rules, so the table does not",
			"have to grow a row per codepoint: accented letters fold through their",
			"Unicode decomposition, every kind of space becomes a plain one, skin",
			"tone modifiers and other invisibles are dropped, and line art is",
			"approximated by its orientation. Whatever survives all of that is",
			"written out as its Unicode name in brackets - the last resort, not",
			"the common case.",
			"",
			"The fold happens on every line sent, so nothing can slip past it.",
			"What you see in your own scrollback is the folded text, which is what",
			"the other side saw.",
		},
	},
	{
		name:    "paste",
		heading: "Pasting something with newlines in it",
		short: []string{
			"M-p (Alt-p) is paste mode: Enter puts in a newline instead of sending,",
			"and ';' ':' ',' '=' insert literally instead of completing names. The",
			"prompt reads 'Paste:' while it is on. M-p again to leave.",
		},
		long: []string{
			"Without it, pasting a block with newlines in it sends a line at a",
			"time, and any ';' or ':' in the text expands as a name - which is how",
			"a pasted snippet turns into a dozen half-messages to people who did",
			"not ask for them.",
			"",
			"M-p toggles paste mode, and the prompt changes to 'Paste:' so you can",
			"see which mode you are in. While it is on:",
			"",
			"  Enter, C-m, C-j   insert a newline",
			"  ; : , =           insert literally, no expansion",
			"  history search    disabled",
			"  M- chords         still work, so M-p gets you back out",
			"",
			"A bracketed paste from the terminal is handled whole rather than as",
			"keystrokes, so a paste that arrives that way needs none of this.",
			"Paste mode is for the case where it does not.",
		},
	},
	{
		name:    "reconnect",
		heading: "You come back where you stopped reading",
		short: []string{
			"When the connection drops, answer y and zlily rejoins - and restores",
			"you to where you had read to, not to the end of what arrived while it",
			"was gone. What you had not read is still ahead of you.",
		},
		long: []string{
			"A dropped connection prompts 'Reconnect?': y or Enter to rejoin, n or",
			"Esc to quit.",
			"",
			"What comes back is not simply the newest output. zlily reports how far",
			"you have read as you page past content, not as it arrives, and the",
			"proxy keeps that position with the session. So a reconnect - and, in",
			"the browser, a page reload - puts you back where you stopped reading,",
			"with the backlog still in front of you rather than scrolled past.",
			"",
			"The session itself survives on the proxy regardless of whether a",
			"client is attached, so the reconnect is joining something that was",
			"never interrupted. See '%help tips share'.",
		},
	},
	{
		name:    "links",
		heading: "URLs in output are clickable",
		short: []string{
			"zlily marks up URLs in the output as real hyperlinks, so a supporting",
			"terminal will let you click one - and a URL wrapped over two lines is",
			"still one link, not two halves.",
		},
		long: []string{
			"Output URLs are wrapped in OSC 8 hyperlinks. In a terminal that",
			"supports them - iTerm2, Kitty, WezTerm, GNOME Terminal, Windows",
			"Terminal and others - they are clickable, and the fragments of a URL",
			"split across a line wrap share a link id so the terminal treats them",
			"as one link on hover rather than as two unrelated pieces. Terminals",
			"without support ignore the markup and show the URL plainly.",
			"",
			"Punctuation that ends a sentence is left out of the link target, so a",
			"URL at the end of a sentence does not carry the full stop with it.",
			"",
			"It is off entirely under GNU screen, which never forwards OSC 8 to the",
			"outer terminal - and whose older builds overflow their escape buffer",
			"on a long URL and print the overflow as text, which corrupts the",
			"display rather than merely failing to link. Very long URLs are shown",
			"as plain text too, since terminals cap what they will parse.",
		},
	},
	{
		name:    "debugview",
		heading: "Watch the protocol go by",
		short: []string{
			"Press Esc then G and the screen splits, showing the live protocol",
			"traffic beside your session - what was sent, what came back, and how",
			"names were completed. PgUp and PgDn scroll it on its own.",
		},
		long: []string{
			"Esc then G, or M-g (Alt-g), toggles a split view: your output on the",
			"left, a live message log on the right showing SEND: and RECV: traffic",
			"and the name lookups behind Tab completion. PgUp and PgDn scroll the",
			"log independently of the conversation.",
			"",
			"'%set debug keys' adds every key event to the same log, which is how",
			"you find out what your terminal is actually sending for a key that is",
			"not doing what you expect. Run it again to stop.",
			"",
			"It is worth a look when something is not behaving: seeing that a send",
			"went out, or that a name resolved to nothing, usually settles which",
			"end the problem is at. For a bug report, '%debug snapshot' packages",
			"this and more into a file - see '%help tips snapshot'.",
			"",
			"Setting ZLILY_WIRELOG=<path> before starting zlily writes a timestamped",
			"transcript of the whole Lily conversation to a file instead.",
		},
	},
	{
		name:    "bots",
		heading: "Write a bot in Python",
		short: []string{
			"clients/python/ in the zlily source is zlilybot, a small library for",
			"driving a Lily session from Python. It can start its own proxy or",
			"attach to one already running.",
		},
		long: []string{
			"zlilybot talks to the same proxy API this client does, so a bot gets",
			"the session handling, the entity database and the name expansion",
			"without reimplementing SLCP.",
			"",
			"  from zlilybot import Bot",
			"",
			"It gives you Bot and Session, wrappers for /fetch, /store, /expand and",
			"/shorten, a Throttle for not flooding, and owner commands ($join,",
			"$quit, $ignore, $unignore, $cmd) that work out of the box.",
			"",
			"ProxyRunner decides where the proxy comes from: mode='spawn' starts a",
			"'zlily server' on a port it picks and shuts it down on exit, while",
			"mode='attach' points at one that is already running - which is how a",
			"bot and your own client can share a session.",
			"",
			"See clients/python/README.md in the zlily source.",
		},
	},
}

// tipsLoadedMsg carries the stored %tips setting back into Update.
type tipsLoadedMsg struct{ off bool }

// loadTipsCmd reads the stored setting. It is a command rather than a straight
// call in New because natively it touches the filesystem, and Init is where the
// rest of the startup I/O already is (see loadCredsCmd).
func loadTipsCmd() tea.Cmd {
	return func() tea.Msg { return tipsLoadedMsg{off: loadTipsOff()} }
}

// handleTipsCommand implements '%tips [on|off]'.
//
// Bare '%tips' shows a tip rather than only reporting the setting, which is a
// small departure from %page and %mouse. Those toggle something you can see the
// effect of; a tip you have to wait a session for is not, and "show me one now"
// is the thing people actually want from the command.
func (m Model) handleTipsCommand(fields []string) (Model, []string) {
	if len(fields) > 2 {
		return m, []string{"Usage: %tips [on|off]"}
	}
	if len(fields) == 2 {
		on, ok := cmdarg.OnOff(fields[1])
		if !ok {
			return m, []string{"Usage: %tips [on|off]"}
		}
		m.tipsEnabled = on
		lines := []string{"Feature tips: " + onOff(on)}
		if err := saveTipsOff(!on); err != nil {
			// Worth saying: the setting is in force now but will not be next
			// time, and silently forgetting it is how someone ends up turning
			// tips off once a day.
			lines = append(lines, "(could not save the setting: "+err.Error()+")")
		}
		if !on {
			lines = append(lines,
				"'%help tips' still lists them, and '%tips' still shows one.")
		}
		return m, lines
	}

	// Bare %tips: a fresh one, whatever the setting. Asking counts as wanting
	// it, and it does not spend the session's automatic tip.
	lines := []string{"Feature tips: " + onOff(m.tipsEnabled), ""}
	return m, append(lines, tipNotice(randomTip())...)
}

// lookupTip finds a tip by name, case-insensitively like every other command
// argument (see internal/cmdarg).
func lookupTip(name string) (tip, bool) {
	folded := cmdarg.Fold(name)
	for _, t := range tips {
		if t.name == folded {
			return t, true
		}
	}
	return tip{}, false
}

// randomTip picks one for this session. Plain uniform choice: a pool this size
// repeats rarely enough that remembering what was shown last time would be more
// machinery than the problem deserves.
func randomTip() tip {
	return tips[rand.IntN(len(tips))]
}

// tipNotice renders a tip as the few lines that land in the scrollback,
// including how to read the rest of it.
func tipNotice(t tip) []string {
	lines := make([]string, 0, len(t.short)+2)
	lines = append(lines, "Tip: "+t.heading)
	lines = append(lines, t.short...)
	lines = append(lines, "See '%help tips "+t.name+"' for more, or '%help tips' for all of them.")
	return lines
}

// tipsListing renders '%help tips': every tip's name and heading, so that a tip
// seen in passing can be found again.
func tipsListing() []string {
	width := 0
	for _, t := range tips {
		if len(t.name) > width {
			width = len(t.name)
		}
	}
	lines := make([]string, 0, len(tips)+6)
	lines = append(lines, "Feature tips. One is shown per session, chosen at random.", "")
	for _, t := range tips {
		lines = append(lines, "  "+t.name+strings.Repeat(" ", width-len(t.name))+"  "+t.heading)
	}
	lines = append(lines,
		"",
		"'%help tips <name>' reads one in full, and '%tips' shows a fresh one now.",
		"'%tips off' stops the per-session tip; put it in a zlilyStartup memo to",
		"keep it off everywhere.",
	)
	return lines
}

// tipHelp renders '%help tips <name>'.
func tipHelp(name string) []string {
	t, ok := lookupTip(name)
	if !ok {
		lines := []string{"No such tip: " + name, "", "Known tips:"}
		return append(lines, tipNameColumns()...)
	}
	out := make([]string, 0, len(t.long)+2)
	out = append(out, t.heading, "")
	return append(out, t.long...)
}

// tipNameColumns lists the tip names a few to a line, for the case where
// someone asked for one that does not exist and wants the shortest possible
// answer rather than the whole listing again.
func tipNameColumns() []string {
	const perLine = 6
	var lines []string
	for i := 0; i < len(tips); i += perLine {
		end := min(i+perLine, len(tips))
		names := make([]string, 0, perLine)
		for _, t := range tips[i:end] {
			names = append(names, t.name)
		}
		lines = append(lines, "  "+strings.Join(names, " "))
	}
	return lines
}
