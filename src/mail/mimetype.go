package mail

// MIMEType is a MIME media type used by the client.
type MIMEType string

const (
	MIMETypeMultipartEncrypted MIMEType = "multipart/encrypted"
	MIMETypeMultipartMixed     MIMEType = "multipart/mixed"
	MIMETypeMultipartSigned    MIMEType = "multipart/signed"
	MIMETypePGPEncrypted       MIMEType = "application/pgp-encrypted"
	MIMETypePGPSignature       MIMEType = "application/pgp-signature"
	MIMETypeOctetStream        MIMEType = "application/octet-stream"
	MIMETypeTextMarkdown       MIMEType = "text/markdown"
	MIMETypeTextPlain          MIMEType = "text/plain"
)

func (t MIMEType) String() string { return string(t) }
