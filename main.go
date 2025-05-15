package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/asticode/go-astisub"
	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

// --- Application Configuration Constants ---
const (
	defaultPlaylistID           = "PL8GTokWa3GEeH8kUkx0rzRWwrzlvO8JaT"
	defaultGeminiModel          = "gemini-1.5-flash-latest"
	defaultTempTranscriptDir    = "./transcripts_temp"
	defaultMaxTranscriptRetries = 3
	defaultTranscriptRetryDelay = 5 * time.Second
	defaultLLMTimeout           = 60 * time.Second
	defaultConcurrencyLimit     = 5
	defaultSummaryWordCount     = 15
	summaryPromptFormat         = "Summarize this video transcript in exactly %d words:\n\nTranscript:\n\"%s\""
	envYoutubeAPIKey            = "YOUTUBE_API_KEY"
	envGeminiAPIKey             = "GEMINI_API_KEY"
	envPlaylistID               = "PLAYLIST_ID"
	envGeminiModel              = "GEMINI_MODEL"
)

// AppConfig (from previous step - unchanged)
type AppConfig struct {
	YoutubeAPIKey        string
	GeminiAPIKey         string
	PlaylistID           string
	GeminiModel          string
	TempTranscriptDir    string
	MaxTranscriptRetries int
	TranscriptRetryDelay time.Duration
	LLMTimeout           time.Duration
	ConcurrencyLimit     int
	SummaryWordCount     int
}

// --- Data Structures ---

// VideoDetails contains essential information about a YouTube video.
type VideoDetails struct { // Renamed from VideoInfo
	ID    string
	Title string
}

// ProcessingResult holds the outcome of fetching and summarizing a video transcript.
type ProcessingResult struct { // Renamed from SummaryInfo
	VideoDetails VideoDetails // Embed VideoDetails
	Summary      string
	Err          error // Changed from string to error type
}

// --- Initialization and Setup --- (Unchanged from previous step)

func loadEnvironmentFile() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Info: .env file not found or could not be loaded: %v. Using system environment variables.", err)
	}
}

func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func initializeAppConfig() (*AppConfig, error) {
	cfg := &AppConfig{
		YoutubeAPIKey:        os.Getenv(envYoutubeAPIKey),
		GeminiAPIKey:         os.Getenv(envGeminiAPIKey),
		PlaylistID:           getEnvWithDefault(envPlaylistID, defaultPlaylistID),
		GeminiModel:          getEnvWithDefault(envGeminiModel, defaultGeminiModel),
		TempTranscriptDir:    defaultTempTranscriptDir,
		MaxTranscriptRetries: defaultMaxTranscriptRetries,
		TranscriptRetryDelay: defaultTranscriptRetryDelay,
		LLMTimeout:           defaultLLMTimeout,
		ConcurrencyLimit:     defaultConcurrencyLimit,
		SummaryWordCount:     defaultSummaryWordCount,
	}
	if cfg.YoutubeAPIKey == "" {
		return nil, fmt.Errorf("%s environment variable must be set", envYoutubeAPIKey)
	}
	return cfg, nil
}

// --- YouTube API Interaction ---

func getYouTubeService(ctx context.Context, apiKey string) (*youtube.Service, error) {
	service, err := youtube.NewService(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("youtube.NewService: %w", err)
	}
	return service, nil
}

// Modified to return []VideoDetails
func getPlaylistVideos(service *youtube.Service, playlistID string) ([]VideoDetails, error) {
	var videos []VideoDetails // Changed type
	nextPageToken := ""
	for {
		call := service.PlaylistItems.List([]string{"snippet", "contentDetails"})
		call = call.PlaylistId(playlistID)
		call = call.MaxResults(50)
		if nextPageToken != "" {
			call = call.PageToken(nextPageToken)
		}
		response, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("PlaylistItems.List call failed for playlist %s: %w", playlistID, err)
		}
		for _, item := range response.Items {
			if item.Snippet != nil && item.ContentDetails != nil && item.ContentDetails.VideoId != "" {
				videos = append(videos, VideoDetails{ // Changed type
					ID:    item.ContentDetails.VideoId,
					Title: item.Snippet.Title,
				})
			} else {
				log.Printf("Warning: Playlist %s: Skipping item ID %s due to missing details.", playlistID, item.Id)
			}
		}
		nextPageToken = response.NextPageToken
		if nextPageToken == "" {
			break
		}
	}
	log.Printf("Fetched %d videos from playlist %s.", len(videos), playlistID)
	return videos, nil
}

// --- Transcript Fetching and Parsing --- (getVideoTranscript unchanged from previous step)
func getVideoTranscript(videoID string, cfg *AppConfig) (string, error) {
	videoURL := "https://www.youtube.com/watch?v=" + videoID
	if err := os.MkdirAll(cfg.TempTranscriptDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp dir %s for video %s: %w", cfg.TempTranscriptDir, videoID, err)
	}

	vttFileNamePattern := filepath.Join(cfg.TempTranscriptDir, videoID+".*.vtt")
	var output []byte
	var err error // This err is for yt-dlp command execution
	var cmd *exec.Cmd

	for attempt := 1; attempt <= cfg.MaxTranscriptRetries; attempt++ {
		log.Printf("Video %s: Transcript fetch attempt %d/%d.", videoID, attempt, cfg.MaxTranscriptRetries)
		cmd = exec.Command("yt-dlp",
			"--write-auto-sub", "--write-sub",
			"--sub-format", "vtt",
			"--sub-langs", "en.*,en",
			"--skip-download",
			"-o", filepath.Join(cfg.TempTranscriptDir, "%(id)s.%(ext)s"),
			videoURL,
		)
		log.Printf("Video %s: Running command: %s", videoID, cmd.String())
		output, err = cmd.CombinedOutput()

		if err == nil {
			log.Printf("Video %s: yt-dlp command successful on attempt %d.", videoID, attempt)
			// Check if successful exit still reported no subtitles in its output
			if strings.Contains(string(output), "no subtitles") || strings.Contains(string(output), "no suitable subtitles found") {
				log.Printf("Video %s: No subtitles found (reported by yt-dlp on successful exit).", videoID)
				return "", nil // No transcript, not an error for the overall process
			}
			break // yt-dlp succeeded and didn't say "no subtitles", proceed to parse
		}
		// yt-dlp command failed (err != nil)
		errMsgForLog := string(output)
		log.Printf("Video %s: yt-dlp attempt %d failed: %v\nOutput: %s", videoID, attempt, err, errMsgForLog)
		if strings.Contains(errMsgForLog, "no subtitles") || strings.Contains(errMsgForLog, "no suitable subtitles found") {
			log.Printf("Video %s: No subtitles found (reported by yt-dlp on failed exit). Will not retry.", videoID)
			return "", nil // No transcript, not an error for the overall process
		}
		if attempt < cfg.MaxTranscriptRetries {
			log.Printf("Video %s: Waiting %v before next transcript fetch attempt.", videoID, cfg.TranscriptRetryDelay)
			time.Sleep(cfg.TranscriptRetryDelay)
		}
	}

	if err != nil { // All retries failed for a reason other than "no subtitles"
		return "", fmt.Errorf("yt-dlp command for video %s failed after %d attempts: %w\nLast Output: %s", videoID, cfg.MaxTranscriptRetries, err, string(output))
	}

	// If we're here, yt-dlp command was successful (err is nil from the loop)
	// and it didn't report "no subtitles" in its stdout/stderr.
	log.Printf("Video %s: yt-dlp output (after successful attempt): %s", videoID, string(output))

	// Parsing logic starts here
	matches, globErr := filepath.Glob(vttFileNamePattern)
	if globErr != nil {
		return "", fmt.Errorf("video %s: error searching VTT pattern %s: %w", videoID, vttFileNamePattern, globErr)
	}
	if len(matches) == 0 {
		vttFileNamePattern = filepath.Join(cfg.TempTranscriptDir, videoID+".vtt") // Fallback
		matches, _ = filepath.Glob(vttFileNamePattern)
		if len(matches) == 0 {
			log.Printf("Video %s: No VTT file found after yt-dlp run (output: %s). File may not have been created despite command success.", videoID, string(output))
			return "", nil // File not found
		}
	}
	vttFilePath := matches[0]
	defer os.Remove(vttFilePath)

	subs, openErr := astisub.OpenFile(vttFilePath)
	if openErr != nil {
		return "", fmt.Errorf("video %s: failed to open/parse VTT file %s: %w", videoID, vttFilePath, openErr)
	}
	var transcriptBuilder strings.Builder
	for _, item := range subs.Items {
		for _, line := range item.Lines {
			for _, lineItem := range line.Items {
				transcriptBuilder.WriteString(lineItem.Text)
				transcriptBuilder.WriteString(" ")
			}
		}
		transcriptBuilder.WriteString(" ")
	}
	fullTranscript := strings.TrimSpace(transcriptBuilder.String())
	if fullTranscript == "" {
		log.Printf("Video %s: Parsed transcript from %s is empty.", videoID, vttFilePath)
		return "", nil
	}
	log.Printf("Video %s: Successfully parsed transcript from %s.", videoID, vttFilePath)
	return fullTranscript, nil
}

// --- LLM Interaction --- (summarizeTranscriptWithGemini unchanged from previous step)
func summarizeTranscriptWithGemini(ctx context.Context, geminiModel *genai.GenerativeModel, transcript string, cfg *AppConfig) (string, error) {
	if transcript == "" {
		return "Transcript was empty, no summary generated.", nil
	}

	prompt := fmt.Sprintf(summaryPromptFormat, cfg.SummaryWordCount, transcript)
	llmCtx, cancel := context.WithTimeout(ctx, cfg.LLMTimeout)
	defer cancel()

	resp, err := geminiModel.GenerateContent(llmCtx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("gemini GenerateContent failed: %w", err)
	}
	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini returned no content candidates")
	}
	summaryPart, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", fmt.Errorf("gemini returned unexpected content part type: %T", resp.Candidates[0].Content.Parts[0])
	}
	return strings.TrimSpace(string(summaryPart)), nil
}

// --- Main Application ---
func main() {
	runStart := time.Now()
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.Printf("Application starting...")

	loadEnvironmentFile()
	cfg, err := initializeAppConfig()
	if err != nil {
		log.Fatalf("CRITICAL: Failed to initialize application configuration: %v", err)
	}

	log.Printf("--- Application Configuration ---")
	log.Printf("Playlist ID: %s", cfg.PlaylistID)
	log.Printf("Gemini Model: %s", cfg.GeminiModel)
	log.Printf("Summary Word Count: %d", cfg.SummaryWordCount)
	log.Printf("Concurrency Limit: %d", cfg.ConcurrencyLimit)
	youtubeKeyStatus := "NOT LOADED"
	if cfg.YoutubeAPIKey != "" {
		youtubeKeyStatus = "LOADED"
	}
	log.Printf("YouTube API Key: [%s]", youtubeKeyStatus)
	geminiKeyStatus := "NOT LOADED - Summarization will be skipped"
	if cfg.GeminiAPIKey != "" {
		geminiKeyStatus = "LOADED"
	}
	log.Printf("Gemini API Key: [%s]", geminiKeyStatus)
	log.Println("-------------------------------")

	ctx := context.Background()
	var geminiClient *genai.GenerativeModel
	if cfg.GeminiAPIKey != "" {
		client, errClient := genai.NewClient(ctx, option.WithAPIKey(cfg.GeminiAPIKey)) // Renamed err to errClient
		if errClient != nil {
			log.Printf("Warning: Failed to create Gemini client (key was present): %v. Summarization will be skipped.", errClient)
		} else {
			geminiClient = client.GenerativeModel(cfg.GeminiModel)
			log.Printf("Successfully initialized Gemini client with model %s.", cfg.GeminiModel)
		}
	}

	youtubeService, err := getYouTubeService(ctx, cfg.YoutubeAPIKey)
	if err != nil {
		log.Fatalf("CRITICAL: Failed to create YouTube service: %v", err)
	}
	log.Printf("Successfully initialized YouTube service.")

	videos, err := getPlaylistVideos(youtubeService, cfg.PlaylistID) // videos is now []VideoDetails
	if err != nil {
		log.Fatalf("CRITICAL: Failed to fetch video details from playlist %s: %v", cfg.PlaylistID, err)
	}
	if len(videos) == 0 {
		log.Printf("No videos found in playlist %s. Exiting.", cfg.PlaylistID)
		return
	}

	log.Printf("--- Processing %d Videos Concurrently (Limit: %d) ---", len(videos), cfg.ConcurrencyLimit)

	var wg sync.WaitGroup
	// resultsChannel now carries ProcessingResult
	resultsChannel := make(chan ProcessingResult, len(videos))
	semaphore := make(chan struct{}, cfg.ConcurrencyLimit)

	for _, video := range videos { // video is VideoDetails
		wg.Add(1)
		semaphore <- struct{}{}

		go func(v VideoDetails, currentCfg *AppConfig, currentGeminiClient *genai.GenerativeModel) {
			defer wg.Done()
			defer func() { <-semaphore }()

			log.Printf("Video %s (%s): Worker started.", v.ID, v.Title)
			// Initialize ProcessingResult with VideoDetails
			currentProcessingResult := ProcessingResult{VideoDetails: v}

			transcript, transcriptErr := getVideoTranscript(v.ID, currentCfg) // transcriptErr
			if transcriptErr != nil {
				log.Printf("Video %s (%s): Could not get transcript: %v", v.ID, v.Title, transcriptErr)
				currentProcessingResult.Err = transcriptErr // Store the error object
				resultsChannel <- currentProcessingResult
				return
			}

			if transcript == "" {
				log.Printf("Video %s (%s): No transcript found or extracted.", v.ID, v.Title)
				currentProcessingResult.Err = fmt.Errorf("no transcript available") // Use error type
			} else {
				log.Printf("Video %s (%s): Successfully fetched transcript.", v.ID, v.Title)
				minValLocal := func(a, b int) int {
					if a < b {
						return a
					}
					return b
				}
				log.Printf("  Transcript snippet for %s: %s...", v.ID, transcript[:minValLocal(100, len(transcript))])

				if currentGeminiClient != nil {
					log.Printf("  Video %s (%s): Attempting to summarize transcript...", v.ID, v.Title)
					summary, summaryErr := summarizeTranscriptWithGemini(ctx, currentGeminiClient, transcript, currentCfg) // summaryErr
					if summaryErr != nil {
						log.Printf("  Video %s (%s): Error summarizing: %v", v.ID, v.Title, summaryErr)
						currentProcessingResult.Err = summaryErr // Store error object
					} else {
						log.Printf("  Video %s (%s): Successfully summarized.", v.ID, v.Title)
						currentProcessingResult.Summary = strings.TrimSpace(summary)
						log.Printf("  Summary for %s: %s", v.ID, currentProcessingResult.Summary)
					}
				} else {
					// Only set error if no other error has occurred yet for this video
					if currentProcessingResult.Err == nil {
						currentProcessingResult.Err = fmt.Errorf("summarization skipped (Gemini client not available)")
					}
					log.Printf("  Video %s (%s): Summarization skipped (Gemini client not available).", v.ID, v.Title)
				}
			}
			resultsChannel <- currentProcessingResult
		}(video, cfg, geminiClient)
	}

	go func() {
		wg.Wait()
		close(resultsChannel)
	}()

	// Results collection needs to handle ProcessingResult
	allResults := make(map[string]ProcessingResult)
	for result := range resultsChannel {
		allResults[result.VideoDetails.ID] = result // Use VideoDetails.ID
	}

	fmt.Println("\n\n--- All Video Summaries (Processed Concurrently) ---")
	successfulSummaries := 0
	videosWithErrors := 0 // Simplified error count

	// Iterate original video list for order
	for _, video := range videos { // video is VideoDetails
		result, ok := allResults[video.ID]
		if !ok {
			log.Printf("CRITICAL: No processing result found for video ID %s, Title: %s.", video.ID, video.Title)
			fmt.Printf("\nVideo ID: %s\nTitle: %s\nStatus/Error: Result missing.\n", video.ID, video.Title)
			fmt.Println("------------------------------------")
			videosWithErrors++
			continue
		}

		fmt.Printf("\nVideo ID: %s\nTitle: %s\n", result.VideoDetails.ID, result.VideoDetails.Title)
		if result.Summary != "" {
			fmt.Printf("Summary (%d words): %s\n", cfg.SummaryWordCount, result.Summary)
			successfulSummaries++
		}
		if result.Err != nil { // Check if there was an error object
			fmt.Printf("Status/Error: %v\n", result.Err) // Print error using %v
			videosWithErrors++
		} else if result.Summary == "" { // No error, but also no summary
			fmt.Println("Status: No summary generated (e.g., transcript was empty or summarization skipped).")
		}
		fmt.Println("------------------------------------")
	}
	fmt.Println("\n--- End of Summaries ---")
	log.Printf("Processing complete. Successful summaries: %d, Videos with errors/no summary: %d, Total videos: %d",
		successfulSummaries, videosWithErrors, len(videos))

	if err := os.RemoveAll(cfg.TempTranscriptDir); err != nil {
		log.Printf("Warning: Failed to remove temporary transcript directory %s: %v", cfg.TempTranscriptDir, err)
	} else {
		log.Printf("Successfully removed temporary transcript directory: %s", cfg.TempTranscriptDir)
	}
	log.Printf("Application finished in %v.", time.Since(runStart))
}
