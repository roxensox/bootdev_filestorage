package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	buffer := make([]byte, 32)
	_, err = rand.Read(buffer)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	thumbnailID := base64.RawURLEncoding.EncodeToString(buffer)

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// Set the max memory usage for a thumbnail image to 10MB
	const maxMemory int64 = 10 << 20
	r.ParseMultipartForm(maxMemory)

	// Get the thumbnail data from the request's formfile
	imgData, imgHeader, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get image data", err)
		return
	}

	mediaType := imgHeader.Header.Get("Content-Type")
	mType, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get content type", err)
		return
	}
	if mType != "image/png" && mType != "image/jpeg" {
		respondWithError(w, http.StatusBadRequest, "Incorrect filetype. Only jpeg and png allowed", err)
		return
	}

	// Read the image data into memory as a byte slice
	imgBytes, err := io.ReadAll(imgData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to read image data", err)
		return
	}

	extension := strings.Split(mediaType, "/")[len(strings.Split(mediaType, "/"))-1]
	fName := fmt.Sprintf("%s.%s", thumbnailID, extension)
	log.Println(fName)
	imgPath := filepath.Join(cfg.assetsRoot, fName)
	imgURL := fmt.Sprintf("http://localhost:%s/assets/%s", os.Getenv("PORT"), fName)

	err = os.WriteFile(imgPath, imgBytes, 0o644)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to write image file", err)
		return
	}

	//imgString := base64.StdEncoding.EncodeToString(imgBytes)
	//imgURL := fmt.Sprintf("data:%s;base64,%s", mediaType, imgString)

	// Gets the stored video data from the SQLite database
	videoMetadata, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get metadata from db", err)
		return
	}

	// Checks if the requesting user is authorized to modify the video data
	if videoMetadata.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User not authorized to modify video", nil)
		return
	}

	// Updates the video's metadata to add a new thumbnail URL
	videoMetadata.ThumbnailURL = &imgURL
	cfg.db.UpdateVideo(videoMetadata)
	respondWithJSON(w, http.StatusAccepted, videoMetadata)
}
