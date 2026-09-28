package bypass

// File is one list, ready to send as a document.
type File struct {
	Name string // e.g. "ru-sites-phone.json"
	Data []byte
}
