package mirrorsource

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"
)

type URLTarget struct {
	lock         sync.RWMutex
	finished     sync.WaitGroup
	errCh        chan<- error
	Src          string
	SrcFmt       string
	FileLoc      string
	BaseFmt      string
	VerifyAction interface{}
	PostAction   interface{}
}

func (t *URLTarget) GetFile(URL string) string {
	if URL == "" {
		URL = t.Src
	}

	var curVer []interface{}
	_, err := fmt.Sscanf(URL, t.SrcFmt, curVer...)
	if err != nil {
		t.errCh <- fmt.Errorf("problem parsing format - %v", err)
	}

	return filepath.Join(t.FileLoc, fmt.Sprintf(t.BaseFmt, curVer...))
}

func (t *URLTarget) Check() {
	t.lock.RLock()
	defer t.lock.RUnlock()
	t.finished.Add(1)
	defer t.finished.Done()

	var curVer []interface{}
	_, err := fmt.Sscanf(t.Src, t.SrcFmt, curVer...)
	if err != nil {
		t.errCh <- fmt.Errorf("problem parsing format - %v", err)
	}

	checkVer := curVer[:]
	for i := len(checkVer) - 1; i >= 0; i-- {
		checkVer[i] = t.incrementPoint(checkVer[i])
		// launch a check for every next version
		t.finished.Add(1)
		go t.PokeURL(fmt.Sprintf(t.SrcFmt, checkVer...))
		// reset it to it's deafut for the next run
		checkVer[i] = t.resetPoint(checkVer[i])
	}
}

func (t *URLTarget) incrementPoint(in interface{}) interface{} {
	switch val := in.(type) {
	case bool:
		// if it's a bool, there is no carry over
		if !val {
			return true
		}
	case int:
		return val + 1
	case uint:
		return val + 1
	case string:
		r, _ := utf8.DecodeLastRuneInString(val)
		return fmt.Sprintf("%s%s", val[:len(val)-1], string(r+1))
	}
	t.errCh <- fmt.Errorf("got value %#v do not know how to increment", in)
	panic("was used on a value that cannot be incremented")
}

func (t *URLTarget) resetPoint(in interface{}) interface{} {
	switch in.(type) {
	case bool:
		return false
	case int:
		return 0
	case uint:
		return 0
	case string:
		return "a"
	}
	t.errCh <- fmt.Errorf("got value %#v do not know how to increment", in)
	panic("was used on a value that cannot be incremented")
}

func (t *URLTarget) PokeURL(URL string) {
	t.lock.RLock()
	defer t.lock.RUnlock()
	defer t.finished.Done()

	resp, err := http.DefaultClient.Head(URL)
	if err != nil {
		t.errCh <- fmt.Errorf("problem checking %s - %v", URL, err)
		return
	}
	// TODO: need some better checks to see what to do about sites that redirect newer stuff to the latest
	if resp.StatusCode == http.StatusFound {
		t.finished.Add(1)
		go t.GetURL(URL)
	}
}

func (t *URLTarget) GetURL(URL string) {
	t.lock.Lock()
	defer t.lock.Unlock()
	defer t.finished.Done()
	var removeFile bool

	f, err := os.OpenFile(t.GetFile(URL), os.O_CREATE|os.O_WRONLY, 0640)
	if err == os.ErrExist {
		t.errCh <- fmt.Errorf("download target already pulled - %s - %s",
			URL, t.GetFile(URL))
		return
	}
	if err != nil {
		t.errCh <- fmt.Errorf("failed to open location for writing - %s - %v",
			t.GetFile(URL), err)
		return
	}
	defer func() {
		name := f.Name()
		err := f.Close()
		if err != nil || removeFile {
			t.errCh <- fmt.Errorf("hit an error closing or previous error - %v", err)
			err := os.Remove(name)
			if err != nil {
				t.errCh <- fmt.Errorf("problem removing the file - %v", err)
			}
		}
	}()

	resp, err := http.Get(URL)
	if err != nil {
		t.errCh <- fmt.Errorf("failed to get target %s - %v", URL, err)
		return
	}
	defer resp.Body.Close()

	_, err = io.Copy(f, resp.Body)
	if err != nil {
		t.errCh <- fmt.Errorf("failed to download file - %v", err)
		removeFile = true
	}
}
