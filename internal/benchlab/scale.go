package benchlab

import (
	"fmt"
	"strconv"

	"github.com/commonhuman-lab/chcrawl/config"
)

// ScaleWorkload is one concrete, parameterized instance of a large-scale
// workload family. Scale's meaning is family-specific: unique-URL count for
// most families, chain depth for S2, response body size in KB for S4.
type ScaleWorkload struct {
	Family string
	Scale  int
	Site   *Site
	Opts   RunOptions
}

func ScaleLabel(n int) string {
	switch {
	case n >= 1_000_000 && n%1_000_000 == 0:
		return strconv.Itoa(n/1_000_000) + "m"
	case n >= 1_000 && n%1_000 == 0:
		return strconv.Itoa(n/1_000) + "k"
	default:
		return strconv.Itoa(n)
	}
}

// Default scale tiers per family, ascending (smallest tier gets the most runs).
var (
	S1Scales = []int{100, 1_000, 10_000, 100_000}
	// S2 stops at 10,000: a 100k-deep chain isn't a practical crawl depth, and
	// being strictly sequential, its cost has no parallelism to amortize.
	S2Scales = []int{100, 1_000, 10_000}
	// S3 scale is reference-link count; unique target count is derived (see
	// s3UniqueCount), staying 1,000 for the larger tiers.
	S3Scales = []int{1_000, 10_000, 100_000}
	// S4 scale is body size in KB (1/5/10/25 MB), not a URL count.
	S4SizesKB = []int{1024, 5120, 10240, 25600}
	S5Scales  = []int{100, 1_000, 10_000, 100_000}
	S6Scales  = []int{1_000, 10_000}
	// S1b: same total unique-URL scale as S1, but no single document holds
	// more than ~1,000 links.
	S1bScales = []int{10_000, 100_000}
	// S1c values are the achieved node counts of full 10-ary trees, not round
	// numbers, since a full b-ary tree can't hit an arbitrary total exactly.
	S1cScales = []int{11_111, 111_111}
)

func ScaleFamilies() []string {
	return []string{
		"S1-wide-flat", "S1b-wide-distributed", "S1c-balanced-tree",
		"S2-deep-chain", "S3-high-duplication", "S4-large-html",
		"S5-parameter-heavy", "S6-mixed-realistic",
	}
}

func DefaultScales(family string) []int {
	switch family {
	case "S1-wide-flat":
		return S1Scales
	case "S1b-wide-distributed":
		return S1bScales
	case "S1c-balanced-tree":
		return S1cScales
	case "S2-deep-chain":
		return S2Scales
	case "S3-high-duplication":
		return S3Scales
	case "S4-large-html":
		return S4SizesKB
	case "S5-parameter-heavy":
		return S5Scales
	case "S6-mixed-realistic":
		return S6Scales
	default:
		return nil
	}
}

// BuildScaleWorkload constructs the Site+RunOptions for one (family, scale)
// pair. The parent process and the re-exec'd -internal-scale-worker
// subprocess both call this so they can't disagree about what a
// (family, scale) actually is.
func BuildScaleWorkload(family string, scale int) (ScaleWorkload, error) {
	switch family {
	case "S1-wide-flat":
		return s1WideFlat(scale), nil
	case "S1b-wide-distributed":
		return s1bWideDistributed(scale), nil
	case "S1c-balanced-tree":
		return s1cBalancedTree(scale), nil
	case "S2-deep-chain":
		return s2DeepChain(scale), nil
	case "S3-high-duplication":
		return s3HighDuplication(scale), nil
	case "S4-large-html":
		return s4LargeHTML(scale), nil
	case "S5-parameter-heavy":
		return s5ParameterHeavy(scale), nil
	case "S6-mixed-realistic":
		return s6MixedRealistic(scale), nil
	default:
		return ScaleWorkload{}, fmt.Errorf("unknown scale family %q", family)
	}
}

// baseScaleOpts gives generous MaxPages/MaxFrontierSize headroom: sizing the
// frontier exactly to the discoverable count risks measuring an artificial
// bottleneck instead of the engine.
func baseScaleOpts(totalDiscoverable int) RunOptions {
	headroom := totalDiscoverable*2 + 1000
	return RunOptions{
		MaxDepth:        8,
		MaxPages:        headroom,
		MaxFrontierSize: headroom,
	}
}

// s1WideFlat: a single root page links to n unique leaf pages. Ground truth:
// n+1 unique pages.
func s1WideFlat(n int) ScaleWorkload {
	site := &Site{
		Name:  "s1-wide-flat-" + ScaleLabel(n),
		Seed:  "/",
		Pages: fanout("/", "/leaf/", n),
	}
	return ScaleWorkload{Family: "S1-wide-flat", Scale: n, Site: site, Opts: baseScaleOpts(n)}
}

func s1bHubCount(n int) int {
	const maxHubs = 100
	if n < maxHubs {
		if n < 1 {
			return 1
		}
		return n
	}
	return maxHubs
}

// s1bWideDistributed disambiguates whether S1's super-linear 10k->100k
// transition comes from giant-document parsing or from the crawler's own
// frontier/scheduler scaling: same total URL scale and shallow shape as S1,
// but no single document holds more than ~n/100 links. Ground truth:
// hubCount + leafCount + 1 unique pages.
func s1bWideDistributed(n int) ScaleWorkload {
	hubCount := s1bHubCount(n)
	leavesPerHub := n / hubCount
	if leavesPerHub < 1 {
		leavesPerHub = 1
	}

	root := PageSpec{Path: "/"}
	pages := make([]PageSpec, 0, n+hubCount+1)
	leafIdx := 0
	for h := 0; h < hubCount; h++ {
		hubPath := "/hub/" + strconv.Itoa(h)
		root.Links = append(root.Links, hubPath)
		hub := PageSpec{Path: hubPath}
		for l := 0; l < leavesPerHub; l++ {
			leafPath := "/leaf/" + strconv.Itoa(leafIdx)
			hub.Links = append(hub.Links, leafPath)
			pages = append(pages, PageSpec{Path: leafPath})
			leafIdx++
		}
		pages = append(pages, hub)
	}
	pages = append(pages, root)

	site := &Site{Name: "s1b-wide-distributed-" + ScaleLabel(n), Seed: "/", Pages: pages}
	total := leafIdx + hubCount
	opts := baseScaleOpts(total)
	opts.MaxDepth = 6
	return ScaleWorkload{Family: "S1b-wide-distributed", Scale: total, Site: site, Opts: opts}
}

// treeDepthForTarget returns the deepest full 10-ary tree (root at depth 0)
// whose node count doesn't exceed target, plus that exact total.
func treeDepthForTarget(target int) (depth, total int) {
	const branch = 10
	total = 1
	levelSize := 1
	for {
		next := levelSize * branch
		if total+next > target {
			return depth, total
		}
		total += next
		levelSize = next
		depth++
	}
}

// s1cBalancedTree is the second S1 topology control: same order-of-magnitude
// URL count as S1/S1b, but a balanced branching tree (branch factor 10)
// instead of a shallow hub fan-out.
func s1cBalancedTree(target int) ScaleWorkload {
	depth, total := treeDepthForTarget(target)

	pathFor := func(id int) string {
		if id == 0 {
			return "/"
		}
		return "/t/" + strconv.Itoa(id)
	}
	pages := make([]PageSpec, 0, total)
	type queued struct{ id, level int }
	queue := []queued{{0, 0}}
	nextID := 1
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		p := PageSpec{Path: pathFor(cur.id)}
		if cur.level < depth {
			for i := 0; i < 10; i++ {
				childID := nextID
				nextID++
				p.Links = append(p.Links, pathFor(childID))
				queue = append(queue, queued{childID, cur.level + 1})
			}
		}
		pages = append(pages, p)
	}

	site := &Site{Name: fmt.Sprintf("s1c-balanced-tree-%s", ScaleLabel(total)), Seed: "/", Pages: pages}
	opts := baseScaleOpts(total)
	opts.MaxDepth = depth + 5
	return ScaleWorkload{Family: "S1c-balanced-tree", Scale: total, Site: site, Opts: opts}
}

// s2DeepChain: a strictly linear chain /page/0 -> ... -> /page/{n-1}, root
// pinned to "/". Ground truth: n unique pages.
func s2DeepChain(n int) ScaleWorkload {
	pages := chain("/page/", n)
	pages[0].Path = "/"
	site := &Site{Name: "s2-deep-chain-" + ScaleLabel(n), Seed: "/", Pages: pages}
	opts := baseScaleOpts(n)
	opts.MaxDepth = n + 10 // the one family whose depth scales with n
	return ScaleWorkload{Family: "S2-deep-chain", Scale: n, Site: site, Opts: opts}
}

// s3UniqueCount is 1/10th of refCount, clamped to [100, 1000].
func s3UniqueCount(refCount int) int {
	u := refCount / 10
	if u < 100 {
		u = 100
	}
	if u > 1000 {
		u = 1000
	}
	if u > refCount {
		u = refCount
	}
	return u
}

// s3HighDuplication: a root page with refCount link references, cycling
// through s3UniqueCount(refCount) distinct target pages via three variant
// forms (canonical path, trailing-slash, URL fragment) that chcrawl's default
// StrictMode canonicalization collapses to the same frontier identity.
// Ground truth: uniqueCount+1 unique pages.
func s3HighDuplication(refCount int) ScaleWorkload {
	unique := s3UniqueCount(refCount)
	root := PageSpec{Path: "/"}
	variant := func(i, v int) string {
		base := "/dup/" + strconv.Itoa(i)
		switch v % 3 {
		case 0:
			return base
		case 1:
			return base + "/"
		default:
			return base + "#section"
		}
	}
	for i := 0; i < refCount; i++ {
		target := i % unique
		root.Links = append(root.Links, variant(target, i))
	}
	pages := make([]PageSpec, 0, unique+1)
	pages = append(pages, root)
	for i := 0; i < unique; i++ {
		pages = append(pages, PageSpec{Path: "/dup/" + strconv.Itoa(i)})
	}
	site := &Site{Name: "s3-high-duplication-" + ScaleLabel(refCount), Seed: "/", Pages: pages}
	return ScaleWorkload{Family: "S3-high-duplication", Scale: refCount, Site: site, Opts: baseScaleOpts(unique)}
}

// S3QueryCanonicalizationDemo shows that chcrawl's canonicalization can
// collapse query-parameter-order variants, but only with SortQueryParams
// enabled — not chcrawl's production default. Reported separately from the
// S3 scaling table.
func S3QueryCanonicalizationDemo() ScaleWorkload {
	const refCount = 2000
	unique := 200
	root := PageSpec{Path: "/"}
	for i := 0; i < refCount; i++ {
		target := i % unique
		if i%2 == 0 {
			root.Links = append(root.Links, fmt.Sprintf("/q/%d?a=1&b=2&c=3", target))
		} else {
			root.Links = append(root.Links, fmt.Sprintf("/q/%d?c=3&b=2&a=1", target))
		}
	}
	pages := make([]PageSpec, 0, unique+1)
	pages = append(pages, root)
	for i := 0; i < unique; i++ {
		pages = append(pages, PageSpec{Path: "/q/" + strconv.Itoa(i)})
	}
	site := &Site{Name: "s3b-query-canonicalization-demo", Seed: "/", Pages: pages}
	opts := baseScaleOpts(unique)
	opts.SortQueryParams = true
	opts.Canonicalization = config.StrictMode
	return ScaleWorkload{Family: "S3b-query-canonicalization-demo", Scale: refCount, Site: site, Opts: opts}
}

// s4LinkCount is fixed across every S4 scale: S4's variable is response body
// size (S1 already measures URL count), so a fixed link set isolates
// parsing/allocation/GC cost from frontier-size cost.
const s4LinkCount = 200

// s4LargeHTML: a root page with s4LinkCount real links, rendered before
// sizeKB of filler bytes, so link discovery survives MaxBodyBytes truncation
// of the tail. Ground truth: s4LinkCount+1 unique pages.
func s4LargeHTML(sizeKB int) ScaleWorkload {
	root := PageSpec{Path: "/", PaddingKB: sizeKB}
	pages := make([]PageSpec, 0, s4LinkCount+1)
	for i := 0; i < s4LinkCount; i++ {
		leaf := "/page/" + strconv.Itoa(i)
		root.Links = append(root.Links, leaf)
		pages = append(pages, PageSpec{Path: leaf})
	}
	pages = append(pages, root)
	site := &Site{Name: fmt.Sprintf("s4-large-html-%dkb", sizeKB), Seed: "/", Pages: pages}
	opts := baseScaleOpts(s4LinkCount)
	// Generous MaxBodyBytes so the largest tier is measured at full size, not
	// truncated by the production 10MiB cap; see S4DefaultBodyCapDemo.
	opts.MaxBodyBytes = int64(sizeKB)*1024*2 + 1<<20
	return ScaleWorkload{Family: "S4-large-html", Scale: sizeKB, Site: site, Opts: opts}
}

// S4DefaultBodyCapDemo builds the largest S4 body (25MB) but measures it at
// chcrawl's production-default MaxBodyBytes (10MiB) to show the cap's real
// effect.
func S4DefaultBodyCapDemo() ScaleWorkload {
	ws := s4LargeHTML(25600)
	ws.Site.Name = "s4-large-html-25600kb-default-bodycap"
	ws.Family = "S4-large-html-default-bodycap"
	ws.Opts.MaxBodyBytes = 0 // 0 -> RunOptions.withDefaults() applies the production default (10MiB)
	return ws
}

// s5ParameterHeavy: a root page linking to n distinct query-parameter
// combinations against the same routed path, each with a unique id so every
// reference is distinct under chcrawl's default (non-sorting)
// canonicalization. Ground truth: n+1 unique pages.
func s5ParameterHeavy(n int) ScaleWorkload {
	root := PageSpec{Path: "/"}
	for i := 0; i < n; i++ {
		root.Links = append(root.Links, fmt.Sprintf("/item?id=%d&cat=%d&ref=src%d&page=%d", i, i%50, i%7, i%3))
	}
	site := &Site{Name: "s5-parameter-heavy-" + ScaleLabel(n), Seed: "/", Pages: []PageSpec{root, {Path: "/item"}}}
	return ScaleWorkload{Family: "S5-parameter-heavy", Scale: n, Site: site, Opts: baseScaleOpts(n)}
}

// s6MixedRealistic composes existing PageSpec primitives at scale:
// shallow-wide branches, shared links, redirects, JS-endpoint references,
// large responses, bad-content-type, 500s, and dead links. ~1% of n is
// special-purpose (split six ways below); the rest share two common links.
func s6MixedRealistic(n int) ScaleWorkload {
	special := n / 100
	if special < 12 {
		special = 12
	}
	if special > n {
		special = n
	}
	normal := n - special

	shared := []string{"/common/nav", "/common/footer"}
	root := PageSpec{Path: "/"}
	pages := make([]PageSpec, 0, n+8)

	for i := 0; i < normal; i++ {
		p := "/page/" + strconv.Itoa(i)
		page := PageSpec{Path: p, Links: append([]string{}, shared...)}
		if i%5 == 0 {
			page.Links = append(page.Links, fmt.Sprintf("/search?q=term%d&page=1", i))
		}
		if i%20 == 0 && i+1 < normal {
			page.Links = append(page.Links, "/page/"+strconv.Itoa(i+1))
		}
		root.Links = append(root.Links, p)
		pages = append(pages, page)
	}
	for _, s := range shared {
		pages = append(pages, PageSpec{Path: s})
	}
	pages = append(pages, PageSpec{Path: "/search"})

	for i := 0; i < special; i++ {
		switch i % 6 {
		case 0:
			p := "/err/" + strconv.Itoa(i)
			root.Links = append(root.Links, p)
			pages = append(pages, PageSpec{Path: p, Status: 500})
		case 1:
			p := "/redirect/" + strconv.Itoa(i)
			target := "/redirect/target/" + strconv.Itoa(i)
			root.Links = append(root.Links, p)
			pages = append(pages, PageSpec{Path: p, Redirect: target})
			pages = append(pages, PageSpec{Path: target})
		case 2:
			p := "/app" + strconv.Itoa(i) + ".js"
			root.Links = append(root.Links, p)
			pages = append(pages, PageSpec{Path: p, JSEndpoints: []JSEndpoint{{Method: "GET", Path: "/api/resource/" + strconv.Itoa(i)}}})
		case 3:
			p := "/large/" + strconv.Itoa(i)
			root.Links = append(root.Links, p)
			pages = append(pages, PageSpec{Path: p, PaddingKB: 256})
		case 4:
			p := "/badtype/" + strconv.Itoa(i)
			root.Links = append(root.Links, p)
			pages = append(pages, PageSpec{Path: p, BadContentType: true})
		case 5: // dead link: no PageSpec registered -> 404
			root.Links = append(root.Links, "/notfound-link/"+strconv.Itoa(i))
		}
	}
	pages = append(pages, root)

	site := &Site{Name: "s6-mixed-realistic-" + ScaleLabel(n), Seed: "/", Pages: pages}
	opts := baseScaleOpts(n)
	opts.MaxDepth = 20
	return ScaleWorkload{Family: "S6-mixed-realistic", Scale: n, Site: site, Opts: opts}
}
