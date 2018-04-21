package main

import (
	"bytes"
	"fmt"
	"net/url"
	"sync"

	"github.com/yhat/scrape"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var validSchemes = map[string]struct{}{
	"":      {},
	"http":  {},
	"https": {},
}

type checker struct {
	fetcher     fetcher
	rootPage    page
	rootURL     *url.URL
	results     chan result
	donePages   concurrentStringSet
	concurrency int
}

func newChecker(s string, c int) (checker, error) {
	f := newFetcher(c)
	p, err := f.Fetch(s)

	if err != nil {
		return checker{}, err
	}

	u, err := url.Parse(s)

	if err != nil {
		return checker{}, err
	}

	return checker{f, p, u, make(chan result, c), newConcurrentStringSet(), c}, nil
}

func (c checker) Results() <-chan result {
	return c.results
}

func (c checker) Check() {
	ps := make(chan page, c.concurrency)
	ps <- c.rootPage

	v := sync.WaitGroup{}
	v.Add(1)
	w := sync.WaitGroup{}

	go func() {
		first := true

		for p := range ps {
			w.Add(1)

			go func(p page) {
				c.checkPage(p, ps)
				w.Done()
			}(p)

			if first {
				first = false
				v.Done()
			}
		}
	}()

	v.Wait()
	w.Wait()

	close(c.results)
}

func (c checker) checkPage(p page, ps chan page) {
	n, err := html.Parse(bytes.NewReader(p.Body()))

	if err != nil {
		c.results <- newResultWithError(p.URL().String(), err)
		return
	}

	ns := scrape.FindAll(n, func(n *html.Node) bool {
		return n.DataAtom == atom.A
	})

	sc, ec := make(chan string, len(ns)), make(chan string, len(ns))
	w := sync.WaitGroup{}

	for _, n := range ns {
		w.Add(1)

		go func(n *html.Node) {
			defer w.Done()

			u, err := url.Parse(scrape.Attr(n, "href"))

			if err != nil {
				ec <- err.Error()
				return
			}

			if _, ok := validSchemes[u.Scheme]; !ok {
				return
			}

			if !u.IsAbs() {
				u = p.URL().ResolveReference(u)
			}

			p, err := c.fetcher.Fetch(u.String())

			if err == nil {
				sc <- fmt.Sprintf("link is alive (%v)", u)

				if !c.donePages.Add(p.URL().String()) && u.Hostname() == c.rootURL.Hostname() {
					ps <- p
				}
			} else {
				ec <- fmt.Sprintf("%v (%v)", err, u)
			}
		}(n)
	}

	w.Wait()

	c.results <- newResult(p.URL().String(), stringChannelToSlice(sc), stringChannelToSlice(ec))
}

func stringChannelToSlice(sc <-chan string) []string {
	ss := make([]string, 0, len(sc))

	for i := 0; i < len(sc); i++ {
		ss = append(ss, <-sc)
	}

	return ss
}