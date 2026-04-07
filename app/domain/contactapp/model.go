package contactapp

import "encoding/json"

// ContactRequest is the body for POST /v1/contact.
type ContactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Message string `json:"message"`
}

func (r *ContactRequest) Decode(data []byte) error {
	return json.Unmarshal(data, r)
}

// ContactResponse is returned on success.
type ContactResponse struct {
	Message string `json:"message"`
}

func (r ContactResponse) Encode() ([]byte, string, error) {
	data, err := json.Marshal(r)
	return data, "application/json", err
}
