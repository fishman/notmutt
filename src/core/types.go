package core

type Message struct {
	ID         string
	ThreadID   string
	Timestamp  int64
	Author     string
	Subject    string
	Tags       []string
	References []string
	Paths      []string
	Atts       []Attachment
}

type Attachment struct {
	Name     string
	MimeType string
	Size     int64
}

type Thread struct {
	ID        string
	LastDate  int64
	Collapsed bool
	Root      *Node
	msgs      []*Message
}

type Node struct {
	Msg      *Message
	Children []*Node
}

type Row struct {
	Msg      *Message
	ThreadID string
	Depth    int
	Root     bool
	Count    int
}
