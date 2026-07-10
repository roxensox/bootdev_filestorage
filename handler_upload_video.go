package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	log.Println("Video handler started")

	// Gets the video ID from the request
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to parse video ID", err)
		return
	}

	// Limits max body size to ~1GB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	defer r.Body.Close()

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	// Gets user ID from JWT
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	// Authorizes user to upload video
	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get metadata from DB", err)
		return
	}
	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User not authorized to modify video", nil)
		return
	}

	fmt.Println("Uploading video", videoID, "by user", userID)

	// Reads upload data
	videoData, videoHeader, err := r.FormFile("video")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			respondWithError(w, http.StatusRequestEntityTooLarge, "Video file too large", err)
			return
		}
		respondWithError(w, http.StatusBadRequest, "Failed to get video data", err)
		return
	}
	defer videoData.Close()

	// Validates upload file type
	mediaType := videoHeader.Header.Get("Content-Type")
	mType, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get content type", err)
		return
	}
	if mType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Incorrect filetype. Only MP4 allowed", fmt.Errorf("Incorrect filetype"))
		return
	}

	// Writes upload to disk, temporarily
	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to make temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Confirms copy was successful
	_, err = io.Copy(tempFile, videoData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to copy video data to temp file", err)
		return
	}
	_, err = tempFile.Stat()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get tempFile info", err)
		return
	}

	// Rewinds file before upload
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to rewind file", err)
		return
	}

	reformatted, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to process video for fast start", err)
		return
	}

	reformattedData, err := os.Open(reformatted)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to open reformatted file", err)
		return
	}

	// Generates random file name for S3
	buffer := make([]byte, 32)
	_, err = rand.Read(buffer)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to generate random file key", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(reformattedData.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to determine aspect ratio", err)
		return
	}

	prefix := ""
	switch aspectRatio {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}

	keyString := fmt.Sprintf("%s/%s.mp4", prefix, base64.RawURLEncoding.EncodeToString(buffer))

	// Uploads to S3
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Body:        reformattedData,
		Key:         aws.String(keyString),
		ContentType: aws.String("video/mp4"),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to upload file to external storage", err)
		return
	}

	// Reflect successful upload in database
	videoURL := fmt.Sprintf("https://%s/%s", os.Getenv("CLOUDFRONT_DOMAIN"), keyString)
	videoMetadata.VideoURL = &videoURL
	cfg.db.UpdateVideo(videoMetadata)

	respondWithJSON(w, http.StatusAccepted, videoMetadata)
}

func getVideoAspectRatio(filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("Invalid file path")
	}

	cmd := exec.Command(
		"ffprobe",
		"-v",
		"error",
		"-print_format",
		"json",
		"-show_streams",
		filePath,
	)

	respBuffer := bytes.Buffer{}
	errBuffer := bytes.Buffer{}

	cmd.Stdout = &respBuffer
	cmd.Stderr = &errBuffer

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to execute command")
	}

	resp := struct {
		Streams []struct {
			Width  int32 `json:"width"`
			Height int32 `json:"height"`
		} `json:"streams"`
	}{}

	if err := json.Unmarshal(respBuffer.Bytes(), &resp); err != nil {
		return "", err
	}

	landscape := (16.0 / 9.0) * 1000.0
	landscapeInt := int(landscape)

	portrait := (9.0 / 16.0) * 1000.0
	portraitInt := int(portrait)

	aspectRatio := (float64(resp.Streams[0].Width) / float64(resp.Streams[0].Height)) * 1000.0
	aspectRatioInt := int(aspectRatio)

	output := ""

	switch aspectRatioInt {
	case landscapeInt:
		output = "16:9"
	case portraitInt:
		output = "9:16"
	default:
		output = "other"
	}

	return output, nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outFile := fmt.Sprintf("%s.processing", filePath)
	cmd := exec.Command(
		"ffmpeg",
		"-i",
		filePath,
		"-c",
		"copy",
		"-movflags",
		"faststart",
		"-f",
		"mp4",
		outFile,
	)

	respBuffer := bytes.Buffer{}
	errBuffer := bytes.Buffer{}

	cmd.Stdout = &respBuffer
	cmd.Stderr = &errBuffer

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("FFMPeg failed: %w: %s", err, errBuffer.String())
	}

	return outFile, nil
}
