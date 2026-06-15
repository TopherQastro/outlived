package outlived

import (
	"context"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/datastore"
	"github.com/pkg/errors"
	"google.golang.org/api/iterator"
)

// Figure is a historical figure.
type Figure struct {
	// Link is the path part of the figure's Wikipedia URL.
	// This also serves as the figure's unique datastore key.
	Link string

	Name, Desc     string
	Born, Died     Date
	DaysAlive      int
	Pageviews      int
	ImgSrc, ImgAlt string

	Updated time.Time
}

func (f *Figure) YDAge() string {
	y, d := f.Died.YDSince(f.Born)

	var dstr string
	if d == 1 {
		dstr = fmt.Sprintf("%d day", d)
	} else {
		dstr = fmt.Sprintf("%d days", d)
	}

	if y == 0 {
		return dstr
	}

	var ystr string
	if y == 1 {
		ystr = fmt.Sprintf("%d year", y)
	} else {
		ystr = fmt.Sprintf("%d years", y)
	}

	return fmt.Sprintf("%s, %s", ystr, dstr)
}

func FiguresAliveFor(ctx context.Context, client *datastore.Client, days, limit int) ([]*Figure, error) {
	q := datastore.NewQuery("Figure").Filter("DaysAlive =", days).Order("-Pageviews")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var figures []*Figure
	_, err := client.GetAll(ctx, q, &figures)
	return figures, errors.Wrap(err, "querying figures")
}

func FiguresAliveForAtMost(ctx context.Context, client *datastore.Client, days, minDays, limit int, byPopularity bool) ([]*Figure, error) {
	// Datastore constraint: when a query has an inequality filter (here
	// "DaysAlive <="), the FIRST Order() must be on that same property.
	// Multiple inequality filters on the SAME property are allowed.
	// So we always order by DaysAlive first at the datastore level, and do
	// any popularity sorting in Go afterward, where there are no such limits.
	q := datastore.NewQuery("Figure").Filter("DaysAlive <=", days)
	if minDays > 0 {
		q = q.Filter("DaysAlive >=", minDays)
	}
	q = q.Order("-DaysAlive").Order("-Pageviews")

	// How many candidates to pull from the datastore before sorting in Go.
	//   - Default (closest-in-age) view: we only need the top `limit` by DaysAlive.
	//   - Fame view: "most famous people you've outlived" could be anywhere below
	//     your age line, so we pull a larger pool and pick the top `limit` by
	//     pageviews in memory.
	fetch := limit
	if byPopularity {
		fetch = 2000 // generous cap; the full pool of outlived figures
	}

	it := client.Run(ctx, q)
	var figures []*Figure
	for len(figures) < fetch {
		var fig Figure
		_, err := it.Next(&fig)
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, errors.Wrap(err, "iterating")
		}
		figures = append(figures, &fig)
	}

	if byPopularity {
		// Most famous first; break ties by who lived longer.
		sort.Slice(figures, func(i, j int) bool {
			if figures[i].Pageviews == figures[j].Pageviews {
				return figures[i].DaysAlive > figures[j].DaysAlive
			}
			return figures[i].Pageviews > figures[j].Pageviews
		})
		if len(figures) > limit {
			figures = figures[:limit]
		}
	} else {
		// Closest-in-age first (longest-lived among those you've outlived);
		// break ties by fame.
		sort.Slice(figures, func(i, j int) bool {
			if figures[i].DaysAlive == figures[j].DaysAlive {
				return figures[i].Pageviews > figures[j].Pageviews
			}
			return figures[i].DaysAlive > figures[j].DaysAlive
		})
	}

	return figures, nil
}

func FiguresDiedOn(ctx context.Context, client *datastore.Client, mon time.Month, day int, limit int) ([]*Figure, error) {
	q := datastore.NewQuery("Figure").Filter("Died.M =", int(mon)).Filter("Died.D =", day).Order("-Pageviews")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var figures []*Figure
	_, err := client.GetAll(ctx, q, &figures)
	return figures, errors.Wrap(err, "querying figures")
}

const multiLimit = 500

func ReplaceFigures(ctx context.Context, client *datastore.Client, figures []*Figure) error {
	// Remove duplicates from figures.
	var (
		seen    = make(map[string]struct{})
		deduped []*Figure
	)
	for _, fig := range figures {
		if _, ok := seen[fig.Link]; ok {
			continue
		}
		seen[fig.Link] = struct{}{}
		deduped = append(deduped, fig)
	}
	figures = deduped

	// TODO(bobg): At least in testing mode, this call to Count (apparently) never returns.
	// before, err := client.Count(ctx, allQ)
	// if err != nil {
	// 	return errors.Wrap(err, "counting figures before replace")
	// }

	keys := make([]*datastore.Key, len(figures))
	for i, fig := range figures {
		keys[i] = &datastore.Key{Kind: "Figure", Name: fig.Link}
	}
	for len(figures) > 0 {
		var (
			nextKeys []*datastore.Key
			nextFigs []*Figure
		)
		if len(figures) > multiLimit {
			keys, nextKeys = keys[:multiLimit], keys[multiLimit:]
			figures, nextFigs = figures[:multiLimit], figures[multiLimit:]
		}
		_, err := client.PutMulti(ctx, keys, figures)
		if err != nil {
			return errors.Wrap(err, "storing figures")
		}
		keys, figures = nextKeys, nextFigs
	}

	return nil
}

const stale = 30 * 24 * time.Hour

func ExpireFigures(ctx context.Context, client *datastore.Client) (int, error) {
	q := datastore.NewQuery("Figure")
	q = q.Filter("Updated <", time.Now().Add(-stale)).KeysOnly()
	keys, err := client.GetAll(ctx, q, nil)
	if err != nil {
		return 0, errors.Wrap(err, "getting stale figures")
	}

	count := 0

	for len(keys) > 0 {
		var nextKeys []*datastore.Key

		if len(keys) > multiLimit {
			keys, nextKeys = keys[:multiLimit], keys[multiLimit:]
		}
		err = client.DeleteMulti(ctx, keys)
		if err != nil {
			return count, errors.Wrap(err, "expiring figures")
		}
		count += len(keys)
		keys = nextKeys
	}
	return count, nil
}