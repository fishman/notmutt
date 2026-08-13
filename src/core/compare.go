package core

// ThreadLess orders threads by last date desc, then id bytes.
func ThreadLess(a, b *Thread) bool {
	if a.LastDate != b.LastDate {
		return a.LastDate > b.LastDate
	}
	return a.ID < b.ID
}

// MsgLess orders messages by date desc, then id bytes.
func MsgLess(a, b *Message) bool {
	if a.Timestamp != b.Timestamp {
		return a.Timestamp > b.Timestamp
	}
	return a.ID < b.ID
}
