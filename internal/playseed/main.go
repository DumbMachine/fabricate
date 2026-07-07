// Command playseed generates the realistic, high-volume fixture for the
// google-play httpmock service. Deterministic (fixed RNG seed + base time) so
// the committed seed.json is stable. The service seeds these rows into SQLite,
// so the data is queryable and mutable (reviews:reply updates a row).
//
// Regenerate (from the repo root):
//
//	go run ./internal/playseed
//
// Output shape (apps + flat reviews):
//
//	{"apps":[{"packageName","displayName"}],
//	 "reviews":[{"reviewId","packageName","author","text","starRating","language",
//	             "device","appVersion","lastModified","developerReply","replyModified"}]}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
)

const (
	rngSeed   = 20260626 // fixed → deterministic output
	baseEpoch = 1_780_000_000
	weekSecs  = 7 * 24 * 3600
)

type fixture struct {
	Apps    []appEntry  `json:"apps"`
	Reviews []reviewRow `json:"reviews"`
}
type appEntry struct {
	PackageName string `json:"packageName"`
	DisplayName string `json:"displayName"`
}
type reviewRow struct {
	ReviewID       string `json:"reviewId"`
	PackageName    string `json:"packageName"`
	Author         string `json:"author"`
	Text           string `json:"text"`
	StarRating     int    `json:"starRating"`
	Language       string `json:"language"`
	Device         string `json:"device"`
	AppVersion     string `json:"appVersion"`
	LastModified   int64  `json:"lastModified"`
	DeveloperReply string `json:"developerReply,omitempty"`
	ReplyModified  int64  `json:"replyModified,omitempty"`
}

func main() {
	out := flag.String("out", "profiles/google-play/reviews-demo/seed.json", "output fixture path")
	flag.Parse()

	rng := rand.New(rand.NewSource(rngSeed))
	apps := appConfigs()

	fx := fixture{}
	for _, a := range apps {
		fx.Apps = append(fx.Apps, appEntry{PackageName: a.pkg, DisplayName: a.display})
		fx.Reviews = append(fx.Reviews, genReviews(rng, a)...)
	}

	raw, err := json.MarshalIndent(fx, "", "  ")
	if err != nil {
		fail(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		fail(err)
	}
	answered := 0
	for _, r := range fx.Reviews {
		if r.DeveloperReply != "" {
			answered++
		}
	}
	fmt.Printf("playseed: wrote %s — %d apps, %d reviews (%d already answered), %d bytes\n",
		*out, len(fx.Apps), len(fx.Reviews), answered, len(raw))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "playseed:", err)
	os.Exit(1)
}

// ---------- app config ----------

type ver struct {
	name   string
	weight int
}
type appCfg struct {
	pkg, display string
	count        int
	versions     []ver
	crashVersion string
	starWeights  [5]int
	lowThemes    []string
	midThemes    []string
	highThemes   []string
	replyRate    map[int]float64
}

func appConfigs() []appCfg {
	return []appCfg{
		{
			pkg: "com.acme.shopping", display: "Acme Shopping", count: 180,
			versions: []ver{{"4.2.1", 60}, {"4.2.0", 25}, {"4.1.5", 15}}, crashVersion: "4.2.1",
			starWeights: [5]int{34, 14, 10, 16, 26},
			lowThemes:   []string{"crash", "login", "checkout", "performance", "ads", "bug"},
			midThemes:   []string{"bug", "performance", "feature", "mixed"},
			highThemes:  []string{"praise", "feature", "ui", "support"},
			replyRate:   map[int]float64{1: 0.45, 2: 0.40, 3: 0.20, 4: 0.10, 5: 0.06},
		},
		{
			pkg: "com.acme.fit", display: "Acme Fitness", count: 95,
			versions: []ver{{"2.1.0", 70}, {"2.0.3", 30}}, crashVersion: "2.1.0",
			starWeights: [5]int{14, 8, 10, 20, 48},
			lowThemes:   []string{"sync", "battery", "performance", "bug"},
			midThemes:   []string{"sync", "feature", "mixed"},
			highThemes:  []string{"praise", "feature", "ui"},
			replyRate:   map[int]float64{1: 0.35, 2: 0.30, 3: 0.18, 4: 0.08, 5: 0.04},
		},
		{
			pkg: "com.acme.wallet", display: "Acme Wallet", count: 70,
			versions: []ver{{"3.4.2", 65}, {"3.4.0", 35}}, crashVersion: "3.4.2",
			starWeights: [5]int{30, 10, 8, 14, 38},
			lowThemes:   []string{"billing", "security", "login", "support", "bug"},
			midThemes:   []string{"billing", "feature", "mixed"},
			highThemes:  []string{"praise", "security", "feature"},
			replyRate:   map[int]float64{1: 0.55, 2: 0.50, 3: 0.25, 4: 0.12, 5: 0.07},
		},
	}
}

// ---------- review generation ----------

func genReviews(rng *rand.Rand, a appCfg) []reviewRow {
	out := make([]reviewRow, 0, a.count)
	for i := 0; i < a.count; i++ {
		star := pickStar(rng, a.starWeights)
		var theme string
		switch {
		case star <= 2:
			theme = pick(rng, a.lowThemes)
		case star == 3:
			theme = pick(rng, a.midThemes)
		default:
			theme = pick(rng, a.highThemes)
		}
		v := pickVersion(rng, a, theme, star)
		device := pick(rng, devices)
		ts := int64(baseEpoch + rng.Intn(weekSecs))
		row := reviewRow{
			ReviewID:     reviewID(rng),
			PackageName:  a.pkg,
			Author:       pick(rng, names),
			Text:         fill(pick(rng, textPool(theme, star)), v.name, device),
			StarRating:   star,
			Language:     pickLang(rng),
			Device:       device,
			AppVersion:   v.name,
			LastModified: ts,
		}
		if rng.Float64() < a.replyRate[star] {
			row.DeveloperReply = pick(rng, devReplies)
			row.ReplyModified = ts + int64(rng.Intn(2*3600)+600)
		}
		out = append(out, row)
	}
	return out
}

func pickStar(rng *rand.Rand, w [5]int) int {
	total := 0
	for _, x := range w {
		total += x
	}
	n := rng.Intn(total)
	for i, x := range w {
		if n < x {
			return i + 1
		}
		n -= x
	}
	return 5
}

func pickVersion(rng *rand.Rand, a appCfg, theme string, star int) ver {
	if star <= 2 && (theme == "crash" || theme == "login") {
		for _, v := range a.versions {
			if v.name == a.crashVersion {
				return v
			}
		}
	}
	total := 0
	for _, v := range a.versions {
		total += v.weight
	}
	n := rng.Intn(total)
	for _, v := range a.versions {
		if n < v.weight {
			return v
		}
		n -= v.weight
	}
	return a.versions[0]
}

func pick(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

func pickLang(rng *rand.Rand) string {
	langs := []struct {
		code string
		w    int
	}{{"en", 62}, {"es", 8}, {"pt-BR", 6}, {"de", 5}, {"fr", 5}, {"hi", 4}, {"ja", 3}, {"it", 3}, {"id", 2}, {"tr", 2}}
	total := 0
	for _, l := range langs {
		total += l.w
	}
	n := rng.Intn(total)
	for _, l := range langs {
		if n < l.w {
			return l.code
		}
		n -= l.w
	}
	return "en"
}

func fill(tmpl, ver, device string) string {
	tmpl = strings.ReplaceAll(tmpl, "{ver}", ver)
	tmpl = strings.ReplaceAll(tmpl, "{device}", device)
	return tmpl
}

func reviewID(rng *rand.Rand) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	b := make([]byte, 40)
	for i := range b {
		b[i] = alpha[rng.Intn(len(alpha))]
	}
	return "gp:AOqpTO" + string(b)
}

// ---------- corpora ----------

var devices = []string{
	"Pixel 8 Pro", "Pixel 8", "Pixel 7", "Pixel 6a", "Samsung Galaxy S23", "Samsung Galaxy S22",
	"Samsung Galaxy A54", "Samsung Galaxy A14", "OnePlus 11", "OnePlus Nord 3", "Xiaomi 13",
	"Redmi Note 12", "Motorola Edge 40", "Moto G Power", "Nothing Phone (2)", "Oppo Reno 10",
}

var names = []string{
	"Priya", "Marcus", "Lena", "Tom", "Aisha", "Diego", "Sara", "Ken", "Noah", "Emma",
	"Yuki", "Chloe", "Omar", "Fatima", "Liam", "Sofia", "Arjun", "Mei", "Carlos", "Hannah",
	"Ibrahim", "Nadia", "Lucas", "Grace", "Wei", "Ana", "David R.", "Jess", "Ravi", "Bea",
	"Tariq", "Ingrid", "Pedro", "Yara", "Sam", "Nina", "Andre", "Leah", "Ravi K.", "Mina",
}

var devReplies = []string{
	"Thanks for the report — we're rolling out a fix shortly. Please update to the latest version.",
	"Sorry for the trouble! Could you email support@acme.app so we can take a closer look?",
	"We hear you and a fix is in the next release. Thanks for your patience.",
	"Appreciate the feedback — we've logged this for the team.",
	"Thank you! Glad you're enjoying it.",
	"This should be resolved in the update we just shipped — let us know if it persists.",
}

func textPool(theme string, star int) []string {
	switch theme {
	case "crash":
		return []string{
			"App crashes on launch right after the {ver} update. Can't use it at all.",
			"Keeps crashing every time I open it since {ver}. Please fix!",
			"Constant crashes on my {device} after the latest update.",
			"Force closes the moment it opens. Worked fine before {ver}.",
			"Crashes immediately, lost everything in my session. Unusable.",
			"Why does this crash so much now? Was fine last week.",
		}
	case "login":
		return []string{
			"Can't sign in since the {ver} update — it just crashes back to the home screen.",
			"Login screen freezes then the app closes. Stuck on my {device}.",
			"Stuck on the login page, keeps saying network error even on wifi.",
			"After updating to {ver} I get logged out and can't get back in.",
			"Sign-in with Google does nothing. Tried reinstalling, no luck.",
		}
	case "checkout":
		return []string{
			"Checkout button does nothing now. Can't complete my order.",
			"Payment fails at the last step every time since the update.",
			"Cart empties itself when I try to check out. Lost my whole order.",
			"Can't apply promo codes anymore, the field is broken.",
			"Order keeps failing with an unknown error at checkout.",
		}
	case "billing":
		return []string{
			"Charged twice for the same subscription this month. Want a refund.",
			"Got billed after I cancelled. Support hasn't responded.",
			"The renewal price went up without any notice. Not okay.",
			"Premium features I paid for are locked again after the update.",
			"Refund request ignored for a week now. Very frustrating.",
		}
	case "security":
		return []string{
			"Asks for way too many permissions. Doesn't feel safe for a wallet app.",
			"Got a suspicious login alert and support was no help. Worried about my data.",
			"Two-factor stopped working, locked out of my own account.",
			"Why does it need access to my contacts? Uninstalling.",
		}
	case "sync":
		return []string{
			"Step sync stopped working this week. Numbers are way off.",
			"Workouts aren't syncing to the cloud since {ver}. Lost a week of data.",
			"Doesn't sync with my watch anymore after the update.",
			"Data keeps disappearing between my phone and tablet.",
		}
	case "battery":
		return []string{
			"Drains my battery like crazy in the background since {ver}.",
			"Phone gets hot and battery dies fast with this app running.",
		}
	case "performance":
		return []string{
			"So slow and laggy after the {ver} update. Takes forever to load.",
			"Constant buffering and freezing on my {device}.",
			"App is sluggish now, scrolling stutters everywhere.",
			"Takes 10 seconds just to open a page. Was snappy before.",
		}
	case "ads":
		return []string{
			"Way too many ads now, one after every tap. Unusable.",
			"Full-screen ads that you can't close for 30 seconds. Ridiculous.",
			"The new ads make the app almost impossible to use.",
		}
	case "bug":
		return []string{
			"Notifications come in late or not at all since the update.",
			"Dark mode is broken — text is white on white on my {device}.",
			"Search returns no results even for things I know are there.",
			"Images don't load half the time. Annoying.",
			"The back button closes the whole app instead of going back.",
		}
	case "feature":
		if star >= 4 {
			return []string{
				"Love the app! Would be great to have a widget on the home screen.",
				"Solid update. Please add a tablet-optimized layout next.",
				"Really good — an export-to-CSV option would make it perfect.",
				"Great app. Wishlist: offline mode and a dark theme toggle.",
			}
		}
		return []string{
			"Decent but really needs an offline mode.",
			"It's okay. Missing basic features like search filters.",
			"Would be 5 stars if it had a proper widget.",
		}
	case "ui":
		return []string{
			"The new design looks clean and modern. Nice work.",
			"Love the redesign, much easier to navigate now.",
			"Beautiful UI and smooth animations on my {device}.",
		}
	case "support":
		if star >= 4 {
			return []string{
				"Support sorted out my issue within a day. Impressed.",
				"Had a problem, emailed support, fixed fast. Great team.",
			}
		}
		return []string{
			"Support never replied to my emails. Disappointed.",
			"Tried contacting support three times, no answer.",
		}
	case "praise":
		return []string{
			"One of the best apps I've used. Fast, reliable, does exactly what I need.",
			"Absolutely love it. Use it every single day.",
			"Works flawlessly on my {device}. Five stars.",
			"Game changer. Can't imagine my routine without it now.",
			"Clean, fast, and no nonsense. Highly recommend.",
			"Been using it for months, rock solid. Thank you devs!",
			"Exactly what I needed and a joy to use.",
		}
	case "mixed":
		return []string{
			"Good app overall but the recent update introduced some bugs.",
			"Works most of the time, occasional hiccups on my {device}.",
			"Useful, though it could be faster.",
			"Pretty good, a few rough edges to polish.",
		}
	default:
		return []string{"Works for me.", "It's fine.", "No complaints."}
	}
}
