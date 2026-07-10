package main

import "testing"

func TestGetVideoAspectRatio(t *testing.T) {
	tests := []struct {
		name    string
		fPath   string
		want    string
		wantErr bool
	}{
		{
			name:    "Valid landscape input",
			fPath:   "samples/boots-video-horizontal.mp4",
			want:    "16:9",
			wantErr: false,
		},
		{
			name:    "Valid portrait input",
			fPath:   "samples/boots-video-vertical.mp4",
			want:    "9:16",
			wantErr: false,
		},
		{
			name:    "Invalid path",
			fPath:   "scramples/boots-video-vertical.mp4",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getVideoAspectRatio(tt.fPath)
			if got != tt.want {
				t.Errorf("getVideoAspectRatio(%q) = %q, want %q", tt.fPath, got, tt.want)
			}
			if (err != nil) != tt.wantErr {
				if tt.wantErr {
					t.Errorf("getVideoAspectRatio(%q) expected error, got none", tt.fPath)
				} else {
					t.Errorf("getVideoAspectRatio(%q) expected no error, got %q", tt.fPath, err.Error())
				}
			}
		})
	}
}
