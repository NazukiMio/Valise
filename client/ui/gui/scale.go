package gui

import "time"

// The scales every view takes its numbers from (docs/DESIGN.md 1, 3): seven
// type sizes, spacing in fours, four radii and three durations. New code
// writes none of its own; theme files cannot change these yet (docs/UI.md 15).
const (
	fsCaption = 12 // explanations, labels; nothing is smaller
	fsSub     = 13 // secondary lines
	fsBody    = 14
	fsTitle3  = 16 // a card's or a section's heading
	fsTitle2  = 20 // a step's heading
	fsTitle1  = 28 // a page's title, and the verdict at rest
	fsVerdict = 36

	spHair    = 4
	spInside  = 8  // inside a group
	spItem    = 12 // between a row's parts
	spGroup   = 16 // between groups
	spSection = 24 // between a page's blocks
	spPage    = 32

	radControl = 6
	radPanel   = 10
	radCard    = 16
	radPill    = 999

	motionFast = 120 * time.Millisecond
	motionBase = 200 * time.Millisecond
	motionSlow = 320 * time.Millisecond
)
