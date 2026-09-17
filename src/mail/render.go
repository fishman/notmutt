package mail

import "notmutt/core"

// RenderMessageBytes renders generated decrypted content without persisting it.
// fallback supplies notmuch's canonical subject when the encrypted inner entity
// intentionally omits transport headers.
func RenderMessageBytes(data []byte, fallback core.Message, mode core.RenderMode, headers bool, width int, labelLinks bool, dark bool, themeBG string, images bool, imgSizes map[string]core.ImgSize) ([]core.Line, string, []string, error) {
	parsed, err := ParseMessageBytes(data)
	if err != nil {
		return nil, "", nil, err
	}
	subject := fallback.Subject
	if subject == "" {
		subject = parsed.Subject
	}
	lines, links := renderMessage(parsed, subject, mode, headers, width, labelLinks, dark, themeBG, images, imgSizes)
	return lines, ViewMime(parsed, mode), links, nil
}
