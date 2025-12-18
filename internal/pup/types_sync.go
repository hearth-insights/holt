package pup

// Decision represents the synchronizer's decision on a claim.
// M5.1: Simplified to Ignore/Bid only (removed Wait - all errors/unready → Ignore)
type Decision int

const (
	DecisionIgnore Decision = iota // Not relevant, conditions not met, or error occurred (Bid Ignore)
	DecisionBid                     // All conditions met (Bid Exclusive)
)

func (d Decision) String() string {
	switch d {
	case DecisionIgnore:
		return "Ignore"
	case DecisionBid:
		return "Bid"
	default:
		return "Unknown"
	}
}
