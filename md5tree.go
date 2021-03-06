package main

import (
	"crypto/md5"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type result struct {
	path string
	sum  [md5.Size]byte
	err  error
}

func walkFile(done <-chan struct{}, root string, recursive bool) (<-chan string, <-chan error) {
	paths := make(chan string)
	errc := make(chan error, 1)

	go func() {
		defer close(paths)
		errc <- filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if info.IsDir() && !recursive && path != root {
				return filepath.SkipDir
			}

			if !info.Mode().IsRegular() {
				return nil
			}

			select {
			case paths <- path:
			case <-done:
				return errors.New("Walk canceled")
			}
			return nil
		})
	}()

	return paths, errc
}

func digester(done <-chan struct{}, paths <-chan string, c chan<- result) {
	for path := range paths {
		data, err := ioutil.ReadFile(path)
		select {
		case c <- result{path, md5.Sum(data), err}:
		case <-done:
			return
		}
	}
}

func sumFile(done <-chan struct{}, root string, recursive bool) (<-chan result, <-chan error) {
	c := make(chan result)

	paths, errc := walkFile(done, root, recursive)

	var wg sync.WaitGroup
	const numDigesters = 20
	wg.Add(numDigesters)

	for i := 0; i < numDigesters; i++ {
		go func() {
			digester(done, paths, c)
			wg.Done()
		}()
	}

	go func() {
		wg.Wait()
		close(c)
	}()

	return c, errc
}

// MD5All reads all the files in the file tree rooted at root and returns a map
// from file path to the MD5 sum of the file's contents.  If the directory walk
// fails or any read operation fails, MD5All returns an error.
func MD5All(root string, recursive bool) (map[string][md5.Size]byte, error) {
	done := make(chan struct{})
	defer close(done)

	c, errc := sumFile(done, root, recursive)

	m := make(map[string][md5.Size]byte)
	for r := range c {
		if r.err != nil {
			return nil, r.err
		}
		m[r.path] = r.sum
	}

	if err := <-errc; err != nil {
		return nil, err
	}
	return m, nil
}

func main() {
	recursive := flag.Bool("r", false, "recursive")
	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage of md5tree: %s\n", "md5tree [path] [-r]")
		flag.PrintDefaults()
	}
	flag.Parse()

	dirPath := flag.Arg(0)
	if len(dirPath) == 0 {
		dirPath = "."
	}

	m, err := MD5All(dirPath, *recursive)
	if err != nil {
		fmt.Println(err)
		return
	}

	var paths []string
	for path := range m {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	for _, path := range paths {
		fmt.Printf("%x   %s\n", m[path], path)
	}
}
