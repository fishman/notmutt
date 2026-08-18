// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import "sort"

// AccountTag is the message's account: the first account tag in the
// tag list (R2 - accounts map to folder-prefix tags). Empty when the
// message carries no account tag. The one definition: the status bar,
// the compose dialogue detection, and the account resolution all use
// it (DRY - never a second copy).
func AccountTag(tags []string, set map[string]bool) string {
	for _, tag := range tags {
		if set[tag] {
			return tag
		}
	}
	return ""
}

// ResolveOps applies ops to tags, then normalizes exclusive groups.
// Per group, when the ops touch it (any op names a member), exactly one
// member survives: the last member-ADD op wins (moves are symmetric -
// +archive on spam untags spam, +inbox on archive moves back), else the
// sole remaining member, else the first present in list order
// (deterministic for legacy mail carrying several folder tags). Ops
// that do not touch a group leave it untouched: pending mail at
// [pending, inbox, unread] keeps its inbox view, soft tags are never
// removed by folder moves.
//
// newTags is the display arm (stage-time rendering): input order
// preserved, with the group winner rendered at the slot of the first
// member the group drops (staging +archive on [inbox, unread] renders
// [archive, unread]); remaining additions append in op order. resolved
// is the apply arm - the minimal op set (symmetric difference, sorted
// by tag) turning the input tags into newTags. A net no-op yields an
// empty resolved arm.
func ResolveOps(tags []string, ops []TagOp, groups []TagGroup) (newTags []string, resolved []TagOp) {
	set := make(map[string]bool, len(tags)+len(ops))
	for _, t := range tags {
		set[t] = true
	}
	states := make([]struct {
		touched bool
		lastAdd string
	}, len(groups))
	for _, op := range ops {
		for gi, g := range groups {
			if memberOf(g, op.Tag) {
				states[gi].touched = true
				if op.Add {
					states[gi].lastAdd = op.Tag
				}
			}
		}
		if op.Add {
			set[op.Tag] = true
		} else {
			delete(set, op.Tag)
		}
	}
	winners := make([]string, len(groups))
	for gi, g := range groups {
		if !states[gi].touched {
			continue
		}
		keep := ""
		if states[gi].lastAdd != "" && set[states[gi].lastAdd] {
			keep = states[gi].lastAdd
		} else {
			for _, t := range g.Tags {
				if set[t] {
					keep = t
					break
				}
			}
		}
		if keep == "" {
			continue
		}
		winners[gi] = keep
		for _, t := range g.Tags {
			if t != keep {
				delete(set, t)
			}
		}
	}
	// first input slot each group drops, for the display order: the
	// winner renders where the member it replaces was
	firstDrop := make([]int, len(groups))
	for gi := range firstDrop {
		firstDrop[gi] = -1
	}
	for gi, g := range groups {
		if winners[gi] == "" {
			continue
		}
		for i, t := range tags {
			if memberOf(g, t) && t != winners[gi] {
				firstDrop[gi] = i
				break
			}
		}
	}
	newTags = make([]string, 0, len(set))
	seen := make(map[string]bool, len(set))
	for i, t := range tags {
		if !set[t] {
			for gi := range groups {
				if winners[gi] != "" && firstDrop[gi] == i && !seen[winners[gi]] {
					newTags = append(newTags, winners[gi])
					seen[winners[gi]] = true
				}
			}
			continue
		}
		if !seen[t] {
			newTags = append(newTags, t)
			seen[t] = true
		}
	}
	for _, op := range ops {
		if set[op.Tag] && !seen[op.Tag] {
			newTags = append(newTags, op.Tag)
			seen[op.Tag] = true
		}
	}
	return newTags, diffTags(tags, newTags)
}

func memberOf(g TagGroup, tag string) bool {
	for _, t := range g.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// diffTags is the symmetric difference of the applied and resolved
// tags as ops, sorted by tag for a deterministic batch.
func diffTags(a, b []string) []TagOp {
	have := make(map[string]bool, len(a))
	for _, t := range a {
		have[t] = true
	}
	want := make(map[string]bool, len(b))
	for _, t := range b {
		want[t] = true
	}
	var ops []TagOp
	for t := range want {
		if !have[t] {
			ops = append(ops, TagOp{Tag: t, Add: true})
		}
	}
	for t := range have {
		if !want[t] {
			ops = append(ops, TagOp{Tag: t})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Tag < ops[j].Tag })
	return ops
}
