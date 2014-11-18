package notify

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rjeczalik/fs"
)

// TODO(rjeczalik): Move to util.go?

func Split(s string) (string, string) {
	if i := LastIndexSep(s); i != -1 {
		return s[:i], s[i+1:]
	}
	return "", s
}

func Base(s string) string {
	if i := LastIndexSep(s); i != -1 {
		return s[i+1:]
	}
	return s
}

// IndexSep TODO
func IndexSep(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == os.PathSeparator {
			return i
		}
	}
	return -1
}

// LastIndexSep TODO
func LastIndexSep(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == os.PathSeparator {
			return i
		}
	}
	return -1
}

// Skip TODO
var Skip = errors.New("skip")

// WalkPathFunc TODO
type WalkPathFunc func(nd Node, isbase bool) error

// WalkFunc TODO
type WalkFunc func(Node) error

// Node TODO
type Node struct {
	Name  string
	Watch Watchpoint
	Child map[string]Node
}

// NewNode TODO
func (nd Node) child(name string) Node {
	if name == "" {
		return nd
	}
	if child, ok := nd.Child[name]; ok {
		return child
	}
	child := Node{
		Name:  nd.Name + sep + name,
		Watch: make(Watchpoint),
		Child: make(map[string]Node),
	}
	nd.Child[name] = child
	return child

}

// NodeSet TODO
type NodeSet []Node

func (p NodeSet) Len() int           { return len(p) }
func (p NodeSet) Less(i, j int) bool { return p[i].Name < p[j].Name }
func (p NodeSet) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func (p NodeSet) Search(nd Node) int {
	return sort.Search(len(p), func(i int) bool { return p[i].Name >= nd.Name })
}

func (p *NodeSet) Add(nd Node) {
	switch i := p.Search(nd); {
	case i == len(*p):
		*p = append(*p, nd)
	case (*p)[i].Name == nd.Name:
	default:
		*p = append(*p, Node{})
		copy((*p)[i+1:], (*p)[i:])
		(*p)[i] = nd
	}
}

func (p *NodeSet) Del(nd Node) {
	if i, n := p.Search(nd), len(*p); i != n && (*p)[i].Name == nd.Name {
		copy((*p)[i:], (*p)[i+1:])
		*p = (*p)[:n-1]
	}
}

// ChanNodesMap TODO
type ChanNodesMap map[chan<- EventInfo]*NodeSet

func (m ChanNodesMap) Add(c chan<- EventInfo, nd Node) {
	if nds, ok := m[c]; ok {
		nds.Add(nd)
	} else {
		m[c] = &NodeSet{nd}
	}
}

func (m ChanNodesMap) Del(c chan<- EventInfo, nd Node) {
	if nds, ok := m[c]; ok {
		if nds.Del(nd); len(*nds) == 0 {
			delete(m, c)
		}
	}
}

// PathError TODO
type PathError struct {
	Name string
}

func (err PathError) Error() string {
	return `notify: invalid path "` + err.Name + `"`
}

// Tree TODO
type Tree struct {
	FS   fs.Filesystem
	Root Node

	cnd  ChanNodesMap
	stop chan struct{}
	os   Interface
}

func (t *Tree) fs() fs.Filesystem {
	if t.FS != nil {
		return t.FS
	}
	return fs.Default
}

func (t *Tree) setos(wat Watcher) {
	if os, ok := wat.(Interface); ok {
		t.os = os
		return
	}
	os := struct {
		Watcher
		Rewatcher
		RecursiveWatcher
		RecursiveRewatcher
	}{wat, t, t, t}
	if rew, ok := wat.(Rewatcher); ok {
		os.Rewatcher = rew
	}
	if rec, ok := wat.(RecursiveWatcher); ok {
		os.RecursiveWatcher = rec
	}
	if recrew, ok := wat.(RecursiveRewatcher); ok {
		os.RecursiveRewatcher = recrew
	}
	t.os = os

}

func (t *Tree) loopdispatch(c <-chan EventInfo) {
	nd, ok := Node{}, false
	for {
		select {
		case ei := <-c:
			parent, name := Split(ei.Path())
			fn := func(it Node, isbase bool) (_ error) {
				// TODO(rjeczalik): rm bool
				if isbase {
					nd = it
				} else {
					it.Watch.Dispatch(ei, true)
				}
				return
			}
			// Send to recursive watchpoints.
			if err := t.TryWalkPath(parent, fn); err != nil {
				// TODO(rjeczalik): Remove after native recursives got implemented.
				panic("[DEBUG] unexpected processing error: " + err.Error())
			}
			// Send to parent watchpoint.
			nd.Watch.Dispatch(ei, false)
			// Try send to self watchpoint.
			if nd, ok = nd.Child[name]; ok {
				nd.Watch.Dispatch(ei, false)
			}
		case <-t.stop:
			return
		}
	}
}

// NewTree TODO
func NewTree(wat Watcher) *Tree {
	c := make(chan EventInfo, 128)
	t := &Tree{
		Root: Node{Child: make(map[string]Node), Watch: make(Watchpoint)},
		cnd:  make(ChanNodesMap),
		stop: make(chan struct{}),
	}
	t.setos(wat)
	t.os.Dispatch(c, t.stop)
	go t.loopdispatch(c)
	return t
}

func (t *Tree) root(p string) (Node, int) {
	vol := filepath.VolumeName(p)
	return t.Root.child(vol), len(vol) + 1
}

// TryLookPath TODO
func (t *Tree) TryLookPath(p string) (it Node, ok bool) {
	// TODO(rjeczalik): os.PathSeparator or enforce callers to not pass separator?
	if p == "" || p == "/" {
		return t.Root, true
	}
	i := 0
	it, i = t.root(p)
	for j := IndexSep(p[i:]); j != -1; j = IndexSep(p[i:]) {
		if it, ok = it.Child[p[i:i+j]]; !ok {
			return
		}
		i += j + 1
	}
	it, ok = it.Child[p[i:]]
	return
}

// LookPath TODO
//
// TODO(rjeczalik): LookPath(p) should be Look(w.Root, p)
func (t *Tree) LookPath(p string) Node {
	// TODO(rjeczalik): os.PathSeparator or enforce callers to not pass separator?
	if p == "" || p == "/" {
		return t.Root
	}
	it, i := t.root(p)
	for j := IndexSep(p[i:]); j != -1; j = IndexSep(p[i:]) {
		it = it.child(p[i : i+j])
		i += j + 1
	}
	return it.child(p[i:])
}

// Look TODO
func (t *Tree) Look(nd Node, p string) Node {
	if nd.Name == p {
		return nd
	}
	if !strings.HasPrefix(p, nd.Name) || p[len(nd.Name)] != os.PathSeparator {
		return t.LookPath(p)
	}
	i := len(nd.Name) + 1
	for j := IndexSep(p[i:]); j != -1; j = IndexSep(p[i:]) {
		nd = nd.child(p[i : i+j])
		i += j + 1
	}
	return nd.child(p[i:])
}

// Del TODO
//
// TODO(rjeczalik):
func (t *Tree) Del(p string) {
	it, i := t.root(p)
	stack := []Node{it}
	for j := IndexSep(p[i:]); j != -1; j = IndexSep(p[i:]) {
		it = it.child(p[i : i+j])
		stack = append(stack, it)
		i += j + 1
	}
	it = it.child(p[i:])
	it.Child = nil
	it.Watch = nil
	name := Base(it.Name)
	for i = len(stack); i > 0; i-- {
		it = stack[i-1]
		// TODO(rjeczalik): Watch[nil] != 0
		// NOTE(rjeczalik): Event empty watchpoints can hold special nil key.
		if child := it.Child[name]; len(child.Watch) > 1 || len(child.Child) != 0 {
			break
		} else {
			child.Child = nil
			child.Watch = nil
		}
		delete(it.Child, name)
		name = Base(it.Name)
	}
}

// TryWalkPath TODO
func (t *Tree) TryWalkPath(p string, fn WalkPathFunc) error {
	ok := false
	it, i := t.root(p)
	for j := IndexSep(p[i:]); j != -1; j = IndexSep(p[i:]) {
		if it, ok = it.Child[p[i:i+j]]; !ok {
			return &os.PathError{
				Op:   "TryWalkPath",
				Path: p[:i+j],
				Err:  os.ErrNotExist,
			}
		}
		switch err := fn(it, false); err {
		case nil:
		case Skip:
			return nil
		default:
			return err
		}
		i += j + 1
	}
	if it, ok = it.Child[p[i:]]; !ok {
		return &os.PathError{
			Op:   "TryWalkPath",
			Path: p,
			Err:  os.ErrNotExist,
		}
	}
	if err := fn(it, true); err != nil && err != Skip {
		return err
	}
	return nil
}

// WalkPath TODO
//
// NOTE(rjeczalik): WalkPath assumes the p is clean.
func (t *Tree) WalkPath(p string, fn WalkPathFunc) error {
	it, i := t.root(p)
	for j := IndexSep(p[i:]); j != -1; j = IndexSep(p[i:]) {
		it = it.child(p[i : i+j])
		switch err := fn(it, false); err {
		case nil:
		case Skip:
			return nil
		default:
			return err
		}
		i += j + 1
	}
	if err := fn(it.child(p[i:]), true); err != nil && err != Skip {
		return err
	}
	return nil
}

// WalkDir TODO
//
// Uses BFS.
func (t *Tree) WalkDir(nd Node, fn WalkFunc) error {
	stack := []Node{nd}
	for n := len(stack); n != 0; n = len(stack) {
		nd, stack = stack[n-1], stack[:n-1]
		f, err := t.fs().Open(nd.Name)
		if err != nil {
			return err
		}
		fis, err := f.Readdir(0)
		f.Close()
		if err != nil {
			return err
		}
		for _, fi := range fis {
			if fi.IsDir() {
				// TODO(rjeczalik): get rid of filepath.Base
				child := nd.child(filepath.Base(fi.Name()))
				switch err := fn(child); err {
				case nil:
					stack = append(stack, child)
				case Skip:
				default:
					return err
				}
			}
		}
	}
	return nil
}

// Walk TODO
//
// Uses BFS.
func (t *Tree) Walk(nd Node, fn WalkFunc) error {
	stack := []Node{nd}
	for n := len(stack); n != 0; n = len(stack) {
		nd, stack = stack[n-1], stack[:n-1]
		for _, child := range nd.Child {
			switch err := fn(child); err {
			case nil:
				stack = append(stack, child)
			case Skip:
			default:
				return err
			}
		}
	}
	return nil
}

// TODO(rjeczalik): Rename.
func (t *Tree) register(nd Node, c chan<- EventInfo, e Event) EventDiff {
	t.cnd.Add(c, nd)
	// TODO(rjeczalik): check if any of the parents are being watched recursively
	// and the event set is sufficient.
	return nd.Watch.Add(c, e)
}

// TODO(rjeczalik): Rename.
func (t *Tree) unregister(nd Node, c chan<- EventInfo, e Event) EventDiff {
	diff := nd.Watch.Del(c, e)
	if diff != None && diff[1] == 0 {
		// TODO(rjeczalik): Use Node for lookup?
		t.Del(nd.Name)
	}
	t.cnd.Del(c, nd)
	return diff
}

// TODO(rjeczalik): Transfer file watchpoint to its directory one?
//
// TODO(rjeczalik): check if any of the parents are being watched recursively
// and the event set is sufficient.
func (t *Tree) watch(p string, c chan<- EventInfo, e Event) (err error) {
	nd := t.LookPath(p)
	diff := t.register(nd, c, e) // TODO(rjeczalik): inline t.register here?
	switch {
	case diff == None:
	case diff[0] == 0:
		err = t.os.Watch(p, diff[1])
	default:
		err = t.os.Rewatch(p, diff[0], diff[1])
	}
	if err != nil {
		t.unregister(nd, c, diff.Event()) // TODO(rjeczalik): test fine-grained revert
	}
	return
}

func (t *Tree) watchrec(p string, c chan<- EventInfo, e Event) error {
	nd := (*Node)(nil)
	// Look up existing, recursive watchpoint already covering the given p.
	err := t.TryWalkPath(p, func(it Node, isbase bool) error {
		if it.Watch.IsRecursive() {
			nd = &it
			return Skip
		}
		return nil
	})
	if nd != nil {
		// Luckily we have already a recursive watchpoint, now we check whether
		// requested event fits in it and rewatch if not.
		switch diff := nd.Watch.AddRecursive(e); {
		case diff == None:
		case diff[0] == 0:
			panic("[DEBUG] Dangling watchpoint: " + nd.Name)
		default:
			if err := t.RecursiveRewatch(nd.Name, nd.Name, diff[0], diff[1]); err != nil {
				nd.Watch.DelRecursive(diff.Event())
				return err
			}
		}
		// TODO(rjeczalik): The diff must be `None`, see comment in watchpoint.go file.
		_ = t.register(t.Look(*nd, p), c, e) // TODO(rjeczalik): inline t.register here?
		return nil
	}
	// If previous lookup did not fail (*os.PathError - no such path in the tree),
	// there is a chance there exist one or more recursive watchpoints in the
	// subtree starting at p - we would need to rewatch those.
	nds := []Node(nil)
	if err == nil {
		_ = nds
	}
	switch {
	case len(nds) == 1:
		// There exists only one recursive, child watchpoint - it's enough to just
		// rewatch it.
		_ = t.RecursiveRewatch // TODO
		return errors.New("TODO(rjeczalik)")
	case len(nds) != 0:
		// There exist multiple recursive, child watchpoints - we need to unwatch
		// all but one, and the last rewatch to new location.
		_ = t.RecursiveUnwatch // TODO
		_ = t.RecursiveRewatch // TODO
		return errors.New("TODO(rjeczalik)")
	default:
		// Make new watchpoint.
		nd := t.LookPath(p)
		diff := t.register(nd, c, e)
		switch {
		case diff == None:
		case diff[0] == 0:
			err = t.os.RecursiveWatch(p, diff[1])
		default:
			err = t.os.RecursiveRewatch(p, p, diff[0], diff[1])
		}
		if err != nil {
			t.unregister(nd, c, e)
			return err
		}
		nd.Watch.AddRecursive(e)
		return nil
	}
}

// Watch TODO
//
// Watch does not support symlinks as it does not care. If user cares, p should
// be passed to os.Readlink first.
func (t *Tree) Watch(p string, c chan<- EventInfo, e ...Event) (err error) {
	if c == nil {
		panic("notify: Watch using nil channel")
	}
	// Expanding with empty event set is a nop.
	if len(e) == 0 {
		return nil
	}
	isrec := false
	if strings.HasSuffix(p, "...") {
		p, isrec = p[:len(p)-3], true
	}
	if p, err = filepath.Abs(p); err != nil {
		return err
	}
	if isrec {
		return t.watchrec(p, c, joinevents(e)|Recursive)
	}
	return t.watch(p, c, joinevents(e))
}

// Stop TODO
func (t *Tree) Stop(c chan<- EventInfo) {
	if nds, ok := t.cnd[c]; ok {
		var err error
		for _, nd := range *nds {
			// TODO(rjeczalik): Handle recursive watchpoints.
			switch diff := t.unregister(nd, c, ^Event(0)); {
			case diff == None:
			case diff[1] == 0:
				err = t.os.Unwatch(nd.Name)
			default:
				err = t.os.Rewatch(nd.Name, diff[0], diff[1])
			}
			if err != nil {
				panic(err)
			}
		}
		delete(t.cnd, c)
	}

}

// Close TODO
//
// TODO(rjeczalik): Make unexported or remove all watchpoints?
func (t *Tree) Close() error {
	close(t.stop)
	return nil
}

// RecursiveWatch implements notify.RecursiveWatcher interface.
func (t *Tree) RecursiveWatch(p string, e Event) error {
	// Before we're able to decide whether we should watch or rewatch p,
	// an watchpoint must be registered for the path.
	// That's why till this point we already have a watchpoint, so we just watch
	// the p.
	if err := t.os.Watch(p, e); err != nil {
		return err
	}
	fn := func(nd Node) error {
		switch diff := nd.Watch.AddRecursive(e); {
		case diff == None:
			return nil
		case diff[0] == 0:
			return t.os.Watch(nd.Name, diff[1])
		default:
			return t.os.Rewatch(nd.Name, diff[0], diff[1])
		}
	}
	return t.WalkDir(t.LookPath(p), fn)
}

// RecursiveUnwatch implements notify.RecursiveWatcher interface.
func (t *Tree) RecursiveUnwatch(p string) error {
	return errors.New("RecurisveUnwatch TODO(rjeczalik)")
}

// Rewatch implements notify.Rewatcher interface.
func (t *Tree) Rewatch(p string, olde, newe Event) error {
	if err := t.os.Unwatch(p); err != nil {
		return err
	}
	return t.os.Watch(p, newe)
}

// RecursiveRewatch implements notify.RecursiveRewatcher interface.
func (t *Tree) RecursiveRewatch(oldp, newp string, olde, newe Event) error {
	if err := t.os.RecursiveUnwatch(oldp); err != nil {
		return err
	}
	return t.os.RecursiveWatch(newp, newe)
}