package main

import (
	"io/ioutil"
	"os"
	"testing"
	"time"
)

func TestContainerdStartStop(t *testing.T) {
	ctd, err := NewContainerd(os.Stderr)
	if err != nil {
		t.Error(err)
	}

	time.Sleep(3 * time.Second)

	err = ctd.Stop()
	if err != nil {
		t.Error(err)
	}
}

func TestContainerdFetch(t *testing.T) {
	ctd, err := NewContainerd(os.Stderr)
	if err != nil {
		t.Error(err)
	}

	err = ctd.Fetch("docker.io/library/alpine:3.5", false)
	if err != nil {
		t.Error(err)
	}

	err = ctd.Stop()
	if err != nil {
		t.Error(err)
	}
}

func TestContainerdStore(t *testing.T) {
	ctd, err := NewContainerd(os.Stderr)
	if err != nil {
		t.Error(err)
	}

	err = ctd.Fetch("docker.io/library/alpine:3.5", false)
	if err != nil {
		t.Error(err)
	}

	f, err := ioutil.TempFile("", "ctd-test")
	if err != nil {
		t.Error(err)
	}
	err = ctd.Store(f)
	if err != nil {
		t.Error(err)
	}

	err = ctd.Stop()
	if err != nil {
		t.Error(err)
	}
}
