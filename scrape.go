package outlived

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bobg/htree"
	"github.com/pkg/errors"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var (
	monthName = []string{
		"",
		"January",
		"February",
		"March",
		"April",
		"May",
		"June",
		"July",
		"August",
		"September",
		"October",
		"November",
		"December",
	}

	dateRegex1 = regexp.MustCompile(`(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d+),\s+(\d+)(.*BC)?`)
	dateRegex2 = regexp.MustCompile(`(\d+)\s+(January|February|March|April|May|June|July|August|September|October|November|December)\s+(\d+)(.*BC)?`)

	bRegex = regexp.MustCompile(`\(b\.\s*\d+\)$`)

	paren = regexp.MustCompile(`^(.*\S)\s*\([^()]*\)$`)

	maybeBornDied  = regexp.MustCompile(`^\s*\(([^()]+)–([^()]+)\)`)
	maybeBornDied2 = regexp.MustCompile(`^\s*\(born\s+([A-Za-z0-9 ]+).*died\s+([A-Za-z0-9 ]+)[^()]*\)`)
)

func ScrapeDay(ctx context.Context, client *http.Client, m time.Month, d int, onPerson func(ctx context.Context, href, title, desc string) error) error {
	pageName := fmt.Sprintf("%s_%d", monthName[m], d)
	resp, _, err := getWikiHTML(ctx, client, pageName)
	if err != nil {
		return errors.Wrapf(err, "getting %s", pageName)
	}
	defer resp.Body.Close()

	tree, err := html.Parse(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "parsing %s", pageName)
	}

	return foreachDeathsUL(tree, func(deaths *html.Node) error {
		for li := deaths.FirstChild; li != nil; li = li.NextSibling {
			if err = ctx.Err(); err != nil {
				return err
			}

			if li.Type != html.ElementNode || li.DataAtom != atom.Li {
				continue
			}

			// Find the node whose text is just a "–" (that's a dash [0x2013], not a hyphen).
			// (In some pages, this is in its own <span>, and in others it's not.)
			dashNode := htree.Find(li, func(n *html.Node) bool {
				txt, _ := htree.Text(n)
				txt = strings.TrimSpace(txt)
				return txt == "–" // dash, not hyphen
			})
			if dashNode == nil {
				continue
			}

			// Find the first sibling of dashNode that's an <a>.
			var aNode *html.Node
			for sib := dashNode.NextSibling; sib != nil; sib = sib.NextSibling {
				if sib.Type != html.ElementNode {
					continue
				}
				if sib.DataAtom != atom.A {
					continue
				}
				aNode = sib
				break
			}
			if aNode == nil {
				continue
			}

			href := htree.ElAttr(aNode, "href")
			href = strings.TrimPrefix(href, "./")

			// We skip certain hrefs. See:
			//   - https://github.com/bobg/outlived/issues/36
			//   - https://github.com/bobg/outlived/issues/47
			//   - https://github.com/bobg/outlived/issues/45
			switch href {
			case "/wiki/Wright_brothers", "/wiki/The_Andrews_Sisters", "/wiki/Billy_the_Kid":
				continue
			}

			title := htree.ElAttr(aNode, "title")
			title = paren.ReplaceAllString(title, "$1")

			b := new(bytes.Buffer)

			for node := aNode.NextSibling; node != nil; node = node.NextSibling {
				if node.Type == html.ElementNode && node.DataAtom == atom.Sup {
					break
				}
				err = htree.WriteText(b, node)
				if err != nil {
					return errors.Wrap(err, "converting description to plain text")
				}
			}

			desc := b.String()
			desc = strings.TrimPrefix(desc, ",")
			if found := bRegex.FindStringIndex(desc); found != nil {
				desc = desc[:found[0]]
			}
			desc = strings.TrimSpace(desc)

			err = onPerson(ctx, href, title, desc)
			if err != nil {
				return errors.Wrapf(err, "on person %s (%s)", title, href)
			}
		}
		return nil
	})
}

func ScrapePerson(
	ctx context.Context,
	client *http.Client,
	href, title, desc string,
	onPerson func(
		ctx context.Context,
		title, desc, href, imgSrc, imgAlt string,
		bornY, bornM, bornD, diedY, diedM, diedD, aliveDays, pageviews int,
	) error,
) error {
	resp, updHref, err := getWikiHTML(ctx, client, href)
	if err != nil {
		return errors.Wrapf(err, "getting %s", href)
	}
	defer resp.Body.Close()
	if updHref != href {
		log.Printf("updating href %s -> %s", href, updHref)
		href = updHref
	}

	tree, err := html.Parse(resp.Body)
	if err != nil {
		return errors.Wrapf(err, "parsing HTML of %s", href)
	}

	fullname, imgSrc, imgAlt, bornY, bornM, bornD, diedY, diedM, diedD, err := parsePerson(ctx, tree, href, title)
	if err != nil {
		return errors.Wrapf(err, "parsing content of %s", href)
	}

	if fullname != "" && fullname != title {
		log.Printf("updating %s -> %s", title, fullname)
		title = fullname
		if ind := strings.Index(title, "\n"); ind > 0 {
			title = strings.TrimSpace(title[:ind])
			log.Printf("truncating to %s", title)
		}
	}

	by := bornY
	if by < 0 {
		by++
	}
	dy := diedY
	if dy < 0 {
		dy++
	}
	born := time.Date(by, time.Month(bornM), bornD, 0, 0, 0, 0, time.UTC)
	died := time.Date(dy, time.Month(diedM), diedD, 0, 0, 0, 0, time.UTC)
	aliveDur := died.Sub(born)
	aliveDays := int(aliveDur / (24 * time.Hour))

	pageviews, err := scrapePageviews(ctx, client, href)
	if err != nil {
		return errors.Wrap(err, "getting pageviews")
	}

	return onPerson(ctx, title, desc, href, imgSrc, imgAlt, bornY, bornM, bornD, diedY, diedM, diedD, aliveDays, pageviews)
}

func parsePerson(ctx context.Context, tree *html.Node, href, title string) (
	fullname, imgSrc, imgAlt string,
	bornY, bornM, bornD, diedY, diedM, diedD int,
	err error,
) {
	infobox := findInfoBox(tree)
	if infobox == nil {
		return parsePersonWithoutInfoBox(ctx, tree, href, title)
	}

	fullname, err = findFullName(infobox)
	if err != nil {
		err = errors.Wrap(err, "finding fullname in infobox")
		return
	}

	imgSrc, imgAlt = findInfoboxImg(infobox)

	bornY, bornM, bornD, err = findDateRow(infobox, "Born")
	if err != nil {
		err = errors.Wrap(err, "finding Born row")
		return
	}
	diedY, diedM, diedD, err = findDateRow(infobox, "Died")
	if err != nil {
		err = errors.Wrap(err, "finding Died row")
		return
	}

	return fullname, imgSrc, imgAlt, bornY, bornM, bornD, diedY, diedM, diedD, err
}

func parsePersonWithoutInfoBox(ctx context.Context, tree *html.Node, href, title string) (
	fullname, imgSrc, imgAlt string,
	bornY, bornM, bornD, diedY, diedM, diedD int,
	err error,
) {
	secNode := htree.FindEl(tree, func(n *html.Node) bool {
		return n.DataAtom == atom.Section
	})
	if secNode == nil {
		return parsePersonWithoutInfoBoxOrSection(ctx, tree, href, title)
	}

	// Look for the first <p> under secNode.
	// Check it for name and dates.
	// If that's no good, look for the second <p> and check that.
	var (
		pNode *html.Node
		tries int
		found bool
	)
	for pNode = secNode.FirstChild; pNode != nil && tries <= 2; pNode = pNode.NextSibling {
		if pNode.Type != html.ElementNode || pNode.DataAtom != atom.P {
			continue
		}

		tries++

		bNode := htree.FindEl(pNode, func(n *html.Node) bool {
			return n.DataAtom == atom.B
		})
		if bNode == nil {
			continue
		}
		fullname, err = htree.Text(bNode)
		if err != nil {
			err = errors.Wrap(err, "converting fullname to text")
			return
		}
		if fullname != title {
			continue
		}

		buf := new(bytes.Buffer)
		for tNode := bNode.NextSibling; tNode != nil; tNode = tNode.NextSibling {
			err = htree.WriteText(buf, tNode)
			if err != nil {
				err = errors.Wrap(err, "converting intro text to plain text")
				return
			}
		}
		tNodeText := buf.String()
		m := maybeBornDied.FindStringSubmatch(tNodeText)
		if len(m) == 0 {
			continue
		}

		bornY, bornM, bornD, err = parseDate(m[1])
		if err != nil {
			continue
		}

		diedY, diedM, diedD, err = parseDate(m[2])
		if err != nil {
			continue
		}

		found = true
		break
	}

	if !found {
		err = fmt.Errorf("no infobox and no suitable intro text in %s", href)
		return
	}

	// Look for the first <figure> under secNode, excluding tables (https://github.com/bobg/outlived/issues/27#issuecomment-552221279).
	// Look for an <img> inside that, and perhaps also a <figcaption> (for the imgAlt).
	secNode = htree.Prune(secNode, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.DataAtom == atom.Table
	})
	figNode := htree.FindEl(secNode, func(n *html.Node) bool {
		return n.DataAtom == atom.Figure
	})
	if figNode == nil {
		return
	}
	imgSrc, imgAlt = findImg(figNode)
	if imgSrc == "" {
		return
	}
	captionEl := htree.FindEl(figNode, func(n *html.Node) bool {
		return n.DataAtom == atom.Figcaption
	})
	if captionEl == nil {
		return
	}
	imgAlt, _ = htree.Text(captionEl)
	return
}

func parsePersonWithoutInfoBoxOrSection(ctx context.Context, tree *html.Node, href, title string) (
	fullname, imgSrc, imgAlt string,
	bornY, bornM, bornD, diedY, diedM, diedD int,
	err error,
) {
	// Look for the first <b>...</b> that contains the title of the page
	// (i.e., the person's name).
	bNode := htree.FindEl(tree, func(n *html.Node) bool {
		if n.DataAtom != atom.B {
			return false
		}
		txt, err := htree.Text(n)
		if err != nil {
			return false
		}
		return txt == title
	})
	if bNode == nil {
		err = fmt.Errorf("no infobox or bolded person name in %s", href)
		return
	}

	fullname, err = htree.Text(bNode)
	if err != nil {
		err = errors.Wrap(err, "converting fullname to text")
		return
	}
	if fullname != title {
		err = fmt.Errorf(`found fullname "%s", which does not match title "%s"`, fullname, title)
		return
	}

	buf := new(bytes.Buffer)
	for tNode := bNode.NextSibling; tNode != nil; tNode = tNode.NextSibling {
		err = htree.WriteText(buf, tNode)
		if err != nil {
			err = errors.Wrap(err, "converting intro text to plain text")
			return
		}
	}
	tNodeText := buf.String()

	m := maybeBornDied.FindStringSubmatch(tNodeText)
	if len(m) == 0 {
		m = maybeBornDied2.FindStringSubmatch(tNodeText)
	}
	if len(m) == 0 {
		err = fmt.Errorf("found bolded person name but no dates in %s", href)
		return
	}

	bornY, bornM, bornD, err = parseDate(m[1])
	if err != nil {
		err = errors.Wrapf(err, "parsing birth date in %s", href)
		return
	}

	diedY, diedM, diedD, err = parseDate(m[2])
	err = errors.Wrapf(err, "parsing death date in %s", href)

	return
}

var nameRegex = regexp.MustCompile(`[^/]+$`)

func scrapePageviews(ctx context.Context, client *http.Client, href string) (int, error) {
	m := nameRegex.FindString(href)
	if m == "" {
		return 0, fmt.Errorf("could not construct pageviews link from href %s", href)
	}

	var (
		now       = time.Now()
		yesterday = now.Add(-24 * time.Hour)
		start     = yesterday.Add(-90 * 24 * time.Hour) // 90 days before yesterday
	)

	// C.f. https://wikimedia.org/api/rest_v1/
	u := fmt.Sprintf("https://wikimedia.org/api/rest_v1/metrics/pageviews/per-article/en.wikipedia.org/all-access/user/%s/daily/%d%02d%02d00/%d%02d%02d00", m, start.Year(), start.Month(), start.Day(), yesterday.Year(), yesterday.Month(), yesterday.Day())
	resp, err := httpGetContext(ctx, client, u)
	if err != nil {
		return 0, errors.Wrapf(err, "fetching %s", u)
	}
	defer resp.Body.Close()

	type (
		respItemType struct {
			Views int
		}

		respType struct {
			Items []*respItemType
		}
	)

	var parsed respType
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&parsed)
	if err != nil {
		return 0, errors.Wrapf(err, "parsing response from %s", u)
	}

	var count int
	for _, item := range parsed.Items {
		count += item.Views
	}

	return count, nil
}

var errNotFound = errors.New("not found")

var smallFontRE = regexp.MustCompile(`font-size: *([0-9]+)%`)

func findFullName(node *html.Node) (string, error) {
	node = htree.Prune(node, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.DataAtom == atom.Sup && htree.ElClassContains(n, "reference")
	})
	fnNode := htree.FindEl(node, func(n *html.Node) bool {
		return htree.ElClassContains(n, "fn")
	})
	if fnNode == nil {
		return "", nil
	}

	// Remove any part of the name in a <small> tag,
	// or in a <span style="...font-size:85%...">.
	// (But keep honorifics.)
	fnNode = htree.Prune(fnNode, func(n *html.Node) bool {
		if n.Type != html.ElementNode {
			return false
		}
		if htree.ElClassContains(n, "honorific-prefix") || htree.ElClassContains(n, "honorific-suffix") {
			return false
		}
		if n.DataAtom == atom.Small {
			return true
		}
		if n.DataAtom != atom.Span {
			return false
		}
		match := smallFontRE.FindStringSubmatch(htree.ElAttr(n, "style"))
		if len(match) < 2 {
			return false
		}
		num, err := strconv.Atoi(match[1])
		if err != nil {
			return false // should be impossible
		}
		return num < 100
	})

	txt, err := htree.Text(fnNode)
	if err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(txt), " "), nil
}

func findDateRow(node *html.Node, label string) (year, mon, day int, err error) {
	if node.Type == html.ElementNode && node.DataAtom == atom.Th {
		var txt string
		txt, err = htree.Text(node)
		if err != nil {
			err = errors.Wrap(err, "converting to text")
			return
		}
		if txt != label {
			return 0, 0, 0, errNotFound
		}
		td := node.NextSibling
		if td == nil || td.Type != html.ElementNode || td.DataAtom != atom.Td {
			return 0, 0, 0, errNotFound
		}
		txt, err = htree.Text(td)
		if err != nil {
			err = errors.Wrap(err, "converting to text")
			return
		}
		return parseDate(txt)
	}
	if node.Type == html.TextNode {
		return 0, 0, 0, errNotFound
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if year, mon, day, err = findDateRow(child, label); err == nil {
			return
		}
	}
	return 0, 0, 0, errNotFound
}

func parseDate(s string) (year, mon, day int, err error) {
	if m := dateRegex1.FindStringSubmatch(s); m != nil {
		return parseDate2(m[3], m[1], m[2], m[4])
	}
	if m := dateRegex2.FindStringSubmatch(s); m != nil {
		return parseDate2(m[3], m[2], m[1], m[4])
	}
	return 0, 0, 0, errNotFound
}

func parseDate2(yearStr, monStr, dayStr, bcStr string) (year, mon, day int, err error) {
	for i := 1; i <= 12; i++ {
		if monStr == monthName[i] {
			mon = i
			break
		}
	}
	if mon == 0 {
		return 0, 0, 0, errors.Wrap(err, "parsing month")
	}
	day, err = strconv.Atoi(dayStr)
	if err != nil {
		return 0, 0, 0, errors.Wrap(err, "parsing day")
	}
	year, err = strconv.Atoi(yearStr) // TODO: range check?
	if err != nil {
		return 0, 0, 0, errors.Wrap(err, "parsing year")
	}
	if bcStr != "" {
		year = -year
	}
	if day < 1 || day > daysInMonth(year, time.Month(mon)) {
		return 0, 0, 0, fmt.Errorf("day %d out of range", day)
	}
	return
}

func foreachDeathsUL(node *html.Node, f func(*html.Node) error) error {
	heading := findDeathsH2(node)
	if heading == nil {
		return errors.New("no Deaths section found")
	}

	var any bool
	for sib := heading.NextSibling; sib != nil; sib = sib.NextSibling {
		if sib.Type != html.ElementNode {
			continue
		}
		if sib.DataAtom == atom.H2 {
			break
		}
		err := htree.FindAllEls(sib, func(n *html.Node) bool { return n.DataAtom == atom.Ul }, func(n *html.Node) error {
			any = true
			return f(n)
		})
		if err != nil {
			return err
		}
	}
	if !any {
		return errors.New("no deaths UL found")
	}
	return nil
}

func findDeathsH2(node *html.Node) *html.Node {
	return htree.FindEl(node, func(n *html.Node) bool {
		if n.DataAtom != atom.H2 {
			return false
		}
		if htree.ElAttr(n, "id") == "Deaths" {
			return true
		}
		span := htree.FindEl(n, func(n2 *html.Node) bool {
			return n2.DataAtom == atom.Span && htree.ElAttr(n2, "id") == "Deaths"
		})
		return span != nil
	})
}

func findInfoBox(node *html.Node) *html.Node {
	return htree.FindEl(node, func(n *html.Node) bool {
		return n.DataAtom == atom.Table && htree.ElClassContains(n, "infobox")
	})
}

// See https://github.com/bobg/outlived/issues/27.
func findInfoboxImg(infobox *html.Node) (src, alt string) {
	thCount := 0
	htree.FindAllChildEls(
		infobox,
		func(n *html.Node) bool {
			if src != "" {
				return true
			}
			if n.DataAtom == atom.Table {
				// Do not descend into nested tables
				return true
			}
			if n.DataAtom == atom.Th {
				thCount++
			}
			if thCount > 1 {
				// Prune nodes under or after the second TH
				return true
			}
			return n.DataAtom == atom.Img
		},
		func(n *html.Node) error {
			if src == "" && n.DataAtom == atom.Img && thCount < 2 {
				src = htree.ElAttr(n, "src")
				alt = htree.ElAttr(n, "alt")
			}
			return nil
		},
	)
	return src, alt
}

func findImg(node *html.Node) (src, alt string) {
	found := htree.FindEl(node, func(n *html.Node) bool {
		return n.DataAtom == atom.Img
	})
	if found != nil {
		return htree.ElAttr(found, "src"), htree.ElAttr(found, "alt")
	}
	return "", ""
}

func httpGetContext(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	// Wikimedia's API requires a descriptive User-Agent that identifies the
	// application and a means of contact. Generic agents may be blocked.
	// EDIT the contact below to your own URL or e-mail.
	req.Header.Set("User-Agent", "Outlived/1.0 (https://outlived.net; contact: tqastro) outlived-scraper")
	return client.Do(req)
}

func getWikiHTML(ctx context.Context, client *http.Client, name string) (*http.Response, string, error) {
	const prefix = "https://en.wikipedia.org/api/rest_v1/page/html/"

	resp, err := httpGetContext(ctx, client, prefix+name)
	if err != nil {
		return nil, "", err
	}

	// Critical: if the server did not return 200, the body is an error page,
	// not the article. Parsing it would yield misleading "no infobox" errors.
	// Surface the real status (and a snippet) instead.
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, "", fmt.Errorf("fetching %s: HTTP %d: %s", prefix+name, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	loc := resp.Header.Get("Content-Location")
	if newName := strings.TrimPrefix(loc, prefix); newName != loc {
		name = newName
	}
	return resp, name, nil
}