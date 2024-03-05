// Imported from
// https://code.google.com/p/go/source/browse/cmd/cover/profile.go?repo=tools&r=c10a9dd5e0b0a859a8385b6f004584cb083a3934

// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Profile represents the profiling data for a specific file.
type Profile struct {
	FileName string
	Mode     string
	Blocks   []ProfileBlock
}

// ProfileBlock represents a single block of profiling data.
type ProfileBlock struct {
	StartLine, StartCol int
	EndLine, EndCol     int
	NumStmt, Count      int
}

type byFileName []*Profile

func (p byFileName) Len() int           { return len(p) }
func (p byFileName) Less(i, j int) bool { return p[i].FileName < p[j].FileName }
func (p byFileName) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func ParseProfiles(in io.Reader, ignore *Ignore) ([]*Profile, error) {
	files := make(map[string]*Profile)
	scanner := bufio.NewScanner(in)
	mode := ""

	for scanner.Scan() {
		line := scanner.Text()
		err := parseLine(&mode, line, files, ignore)
		if err != nil {
			return nil, err
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan profiles: %w", err)
	}

	err := mergeSameLocationSamples(files, mode)
	if err != nil {
		return nil, err
	}

	profiles := generateSortedProfilesSlice(files)

	return profiles, nil
}

func parseLine(mode *string, line string, files map[string]*Profile, ignore *Ignore) error {
	if *mode == "" {
		const prefix = "mode: "

		if !strings.HasPrefix(line, prefix) || line == prefix {
			return fmt.Errorf("bad mode line: %s", line)
		}
		*mode = line[len(prefix):]
		return nil
	}
	match := lineRe.FindStringSubmatch(line)
	if match == nil {
		return nil
	}
	filename := match[1]
	if ignore.Match(filename, nil) {
		return nil
	}
	profile := files[filename]
	if profile == nil {
		profile = &Profile{
			FileName: filename,
			Mode:     *mode,
		}
		files[filename] = profile
	}

	profile.Blocks = append(profile.Blocks, ProfileBlock{
		StartLine: toInt(match[2]),
		StartCol:  toInt(match[3]),
		EndLine:   toInt(match[4]),
		EndCol:    toInt(match[5]),
		NumStmt:   toInt(match[6]),
		Count:     toInt(match[7]),
	})

	return nil
}

func mergeSameLocationSamples(files map[string]*Profile, mode string) error {
	for _, profile := range files {
		sort.Sort(blocksByStart(profile.Blocks))
		blockNo := 1

		for blockIndex := 1; blockIndex < len(profile.Blocks); blockIndex++ {
			currentBlock := profile.Blocks[blockIndex]
			last := profile.Blocks[blockNo-1]
			if currentBlock.StartLine == last.StartLine &&
				currentBlock.StartCol == last.StartCol &&
				currentBlock.EndLine == last.EndLine &&
				currentBlock.EndCol == last.EndCol {
				if currentBlock.NumStmt != last.NumStmt {
					return fmt.Errorf("inconsistent NumStmt: changed from %d to %d", last.NumStmt, currentBlock.NumStmt)
				}
				if mode == "set" {
					profile.Blocks[blockNo-1].Count |= currentBlock.Count
				} else {
					profile.Blocks[blockNo-1].Count += currentBlock.Count
				}

				continue
			}
			profile.Blocks[blockNo] = currentBlock
			blockNo++
		}
		profile.Blocks = profile.Blocks[:blockNo]
	}
	return nil
}

func generateSortedProfilesSlice(files map[string]*Profile) []*Profile {
	profiles := make([]*Profile, 0, len(files))

	for _, profile := range files {
		profiles = append(profiles, profile)
	}

	sort.Sort(byFileName(profiles))
	return profiles
}

type blocksByStart []ProfileBlock

func (b blocksByStart) Len() int      { return len(b) }
func (b blocksByStart) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b blocksByStart) Less(i, j int) bool {
	bi, bj := b[i], b[j]
	return bi.StartLine < bj.StartLine || bi.StartLine == bj.StartLine && bi.StartCol < bj.StartCol
}

var lineRe = regexp.MustCompile(`^(.+):([0-9]+).([0-9]+),([0-9]+).([0-9]+) ([0-9]+) ([0-9]+)$`)

func toInt(s string) int {
	i, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return i
}

// Boundary represents the position in a source file of the beginning or end of a
// block as reported by the coverage profile. In HTML mode, it will correspond to
// the opening or closing of a <span> tag and will be used to colorize the source.
type Boundary struct {
	Offset int     // Location as a byte offset in the source file.
	Start  bool    // Is this the start of a block?
	Count  int     // Event count from the cover profile.
	Norm   float64 // Count normalized to [0..1].
}

func (p *Profile) getMaxCount() int {
	maxCount := 0
	for _, b := range p.Blocks {
		if b.Count > maxCount {
			maxCount = b.Count
		}
	}
	return maxCount
}

func (p *Profile) normalize(count, maxCount int) float64 {
	if maxCount <= 1 {
		return 0.8 // Profile is in"set" mode; we want a heat map. Use cov8 in the CSS.
	} else if count > 0 {
		return math.Log(float64(count)) / math.Log(float64(maxCount))
	}
	return 0
}

func (p *Profile) createBoundary(offset int, start bool, count, maxCount int) Boundary {
	boundary := Boundary{Offset: offset, Start: start, Count: count}
	if !start || count == 0 {
		return boundary
	}
	boundary.Norm = p.normalize(count, maxCount)
	return boundary
}

func (p *Profile) Boundaries(src []byte) []Boundary {
	var boundaries []Boundary
	maxCount := p.getMaxCount()

	line, col := 1, 1

	for si, bi := 0, 0; si < len(src) && bi < len(p.Blocks); {
		b := p.Blocks[bi]
		if b.StartLine == line && b.StartCol == col {
			boundaries = append(boundaries, p.createBoundary(si, true, b.Count, maxCount))
		}
		if b.EndLine == line && b.EndCol == col {
			boundaries = append(boundaries, p.createBoundary(si, false, 0, maxCount))
			bi++

			continue
		}
		if src[si] == '\n' {
			line++
			col = 0
		}
		col++
		si++
	}
	sort.Sort(boundariesByPos(boundaries))
	return boundaries
}

type boundariesByPos []Boundary

func (b boundariesByPos) Len() int      { return len(b) }
func (b boundariesByPos) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b boundariesByPos) Less(i, j int) bool {
	if b[i].Offset == b[j].Offset {
		return !b[i].Start && b[j].Start
	}
	return b[i].Offset < b[j].Offset
}
