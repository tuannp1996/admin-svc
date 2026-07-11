package topic

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "three words", value: "Phí SMS Banking", wantErr: true},
		{name: "four words", value: "Phí dịch vụ SMS Banking", wantErr: false},
		{name: "extra whitespace", value: "  Phí\tdịch vụ  SMS\nBanking ", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.value); (err != nil) != tt.wantErr {
				t.Fatalf("Validate(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}
