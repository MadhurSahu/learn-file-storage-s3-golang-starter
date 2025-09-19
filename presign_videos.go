package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}

	parts := strings.Split(*video.VideoURL, ",")

	if len(parts) != 2 {
		return database.Video{}, errors.New("invalid video url")
	}

	signedURL, err := generatePresignedURL(cfg.s3Client, parts[0], parts[1], 1*time.Hour)
	if err != nil {
		return database.Video{}, err
	}

	video.VideoURL = &signedURL
	return video, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	preSignClient := s3.NewPresignClient(s3Client)
	preSignParams := &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}

	req, err := preSignClient.PresignGetObject(context.Background(), preSignParams, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", err
	}

	return req.URL, nil
}
