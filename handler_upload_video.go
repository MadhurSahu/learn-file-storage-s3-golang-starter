package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxSize = 1 << 30

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized), err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized), err)
		return
	}

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, http.StatusText(http.StatusUnauthorized), err)
		return
	}

	err = r.ParseMultipartForm(maxSize)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse multipart form", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse media type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Only mp4 allowed", err)
		return
	}

	randomBytes := make([]byte, 32)
	_, err = rand.Read(randomBytes)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't generate thumbnail name", err)
		return
	}

	extension := strings.Split(mediaType, "/")[1]
	videoName := base64.RawURLEncoding.EncodeToString(randomBytes) + "." + extension
	videoFile, err := os.CreateTemp("", videoName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create video file", err)
		return
	}
	defer os.Remove(videoFile.Name())

	_, err = io.Copy(videoFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy file", err)
		return
	}

	_, err = videoFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't seek file", err)
		return
	}

	orientation, err := getVideoOrientation(videoFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video orientation", err)
		return
	}

	fastVideoPath, err := processVideoForFastStart(videoFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't process video for fast start", err)
		return
	}
	defer os.Remove(fastVideoPath)

	fastVideoFile, err := os.Open(fastVideoPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't open video file", err)
		return
	}

	videoKeyName := fmt.Sprintf("%s/%s", orientation, videoName)
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &videoKeyName,
		Body:        fastVideoFile,
		ContentType: &mediaType,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't upload video", err)
		return
	}

	videoURL := fmt.Sprintf("https://%s/%s", cfg.s3CfDistribution, videoKeyName)
	video.VideoURL = &videoURL

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}

func getVideoOrientation(filePath string) (string, error) {
	type VideoStream struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	var data bytes.Buffer
	var videoStream VideoStream

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	cmd.Stdout = &data
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	err = json.Unmarshal(data.Bytes(), &videoStream)
	if err != nil {
		return "", err
	}

	if len(videoStream.Streams) == 0 {
		return "", fmt.Errorf("no streams found")
	}

	aspectRatio := videoStream.Streams[0].Width / videoStream.Streams[0].Height
	switch aspectRatio {
	case 16 / 9:
		return "landscape", nil
	case 9 / 16:
		return "portrait", nil
	default:
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outFilePath := filePath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outFilePath)

	err := cmd.Run()
	if err != nil {
		return "", err
	}

	return outFilePath, err
}
