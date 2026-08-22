package replay

import "testing"

func TestParsePricing(t *testing.T) {
	tests := []struct {
		name       string
		spec       string
		wantModel  string
		wantInput  float64
		wantOutput float64
		wantErr    bool
	}{
		{
			name:       "valid",
			spec:       "gpt-4o:5,15",
			wantModel:  "gpt-4o",
			wantInput:  5,
			wantOutput: 15,
		},
		{
			name:    "missing colon",
			spec:    "gpt-4o-5,15",
			wantErr: true,
		},
		{
			name:    "empty model",
			spec:    ":5,15",
			wantErr: true,
		},
		{
			name:    "wrong price count",
			spec:    "gpt-4o:5",
			wantErr: true,
		},
		{
			name:    "invalid input price",
			spec:    "gpt-4o:abc,15",
			wantErr: true,
		},
		{
			name:    "invalid output price",
			spec:    "gpt-4o:5,abc",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, in, out, err := ParsePricing(tt.spec)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParsePricing(%q) error = %v, wantErr %v", tt.spec, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if model != tt.wantModel || in != tt.wantInput || out != tt.wantOutput {
				t.Errorf("ParsePricing(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tt.spec, model, in, out, tt.wantModel, tt.wantInput, tt.wantOutput)
			}
		})
	}
}
