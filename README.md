# Summify: YouTube Playlist Summarizer

Summify is a command-line tool written in Go that processes a YouTube playlist, fetches video transcripts, and uses a Generative AI model (Google's Gemini) to provide a concise summary for each video.

## Features

* Fetches all video details (ID, Title) from a specified YouTube playlist.
* Downloads video transcripts using `yt-dlp` (with retries for robustness).
* Parses VTT subtitle files to extract clean transcript text.
* Sends transcripts to a Google Gemini model for summarization.
* Processes videos concurrently to speed up execution.
* Configurable via environment variables (API keys, playlist ID, etc.).
* Graceful error handling for individual video processing.

## Prerequisites

Before you can run Summify, you need the following installed:

1.  **Go:** Version 1.18 or higher (due to generics usage, if any were introduced, or generally for modern Go features). Ensure Go is correctly installed and configured in your system's PATH.
2.  **yt-dlp:** This tool is used to download video transcripts.
    * Installation via pip (Python package installer) is common:
        ```bash
        pip install yt-dlp
        ```
    * Alternatively, download a release binary from the [yt-dlp GitHub page](https://github.com/yt-dlp/yt-dlp/releases) and ensure it's in your system's PATH.
3.  **ffmpeg (Recommended for yt-dlp):** While not strictly required by Summify for VTT parsing, `yt-dlp` often recommends it for handling various media formats and can sometimes use it for subtitle extraction or conversion.
    * Installation instructions can be found on the [ffmpeg website](https://ffmpeg.org/download.html).

## Setup

1.  **Clone the Repository:**
    ```bash
    git clone https://github.com/YousafRaja/Summify
    cd summify
    ```
    For now, ensure `main.go` is in your project directory.

2.  **Install Go Dependencies:**
    Navigate to your project directory in the terminal and run:
    ```bash
    go mod tidy
    ```
    This will download and install the necessary Go packages defined in the `go.mod` file (which `go mod tidy` will create/update based on imports in `main.go`).

3.  **API Keys:**
    You will need API keys for:
    * **YouTube Data API v3:** To fetch playlist and video information.
        * Go to the [Google Cloud Console](http://googleusercontent.com/cloud.google.com/1).
        * Create a new project or select an existing one.
        * Enable the "YouTube Data API v3".
        * Create an API key under "Credentials".
    * **Google Generative AI (Gemini API):** To summarize transcripts.
        * Go to [Google AI Studio](http://googleusercontent.com/aistudio.google.com/1) or the Google Cloud Console.
        * Obtain an API key for the Gemini API. Ensure the model you intend to use (e.g., `gemini-1.5-flash-latest`) is enabled for your key/project.

4.  **Configuration via `.env` file:**
    Create a file named `.env` in the root of the project directory. This file will store your API keys and other optional configurations. **Important: Add `.env` to your `.gitignore` file to prevent committing your secrets.**

    Example `.env` file content:
    ```env
    # Required API Keys
    YOUTUBE_API_KEY="YOUR_YOUTUBE_DATA_API_KEY_HERE"
    GEMINI_API_KEY="YOUR_GEMINI_API_KEY_HERE"

    # Optional Overrides (defaults are used if these are not set)
    # PLAYLIST_ID="YOUR_TARGET_YOUTUBE_PLAYLIST_ID"
    # GEMINI_MODEL="gemini-1.0-pro" # Or another compatible model
    ```

    * **`YOUTUBE_API_KEY`**: Your API key for the YouTube Data API.
    * **`GEMINI_API_KEY`**: Your API key for the Gemini model. If this is not provided, summarization will be skipped.
    * **`PLAYLIST_ID` (Optional)**: The ID of the YouTube playlist you want to summarize. If not set, a default playlist ID from the code will be used.
    * **`GEMINI_MODEL` (Optional)**: The specific Gemini model to use for summarization (e.g., `gemini-1.5-flash-latest`, `gemini-1.0-pro`). Defaults to `gemini-1.5-flash-latest`.

## Usage

Once set up, you can run the application from your terminal in the project directory:

1.  **Run directly:**
    ```bash
    go run main.go
    ```

2.  **Build and then run the executable:**
    ```bash
    go build -o summify
    ./summify
    ```
    (On Windows, this would be `summify.exe`)

The tool will:
* Load configuration.
* Initialize API clients.
* Fetch videos from the specified playlist.
* Concurrently process each video to:
    * Download its transcript (using `yt-dlp`).
    * Send the transcript to the Gemini API for a summary (e.g., "Summarize this video transcript in exactly 15 words").
* Print the video title and its summary (or any errors encountered during processing) to the console.
* Log detailed progress and errors.
* Clean up temporary transcript files.

## How it Works

1.  **Configuration Loading:** Reads API keys and other settings from environment variables, with support for a `.env` file for local development.
2.  **YouTube API Client:** Uses the official Google API client for Go to interact with the YouTube Data API v3 to list playlist items.
3.  **Transcript Fetching:**
    * Invokes the `yt-dlp` command-line tool as an external process to download available VTT (Web Video Text Tracks) subtitles for each video.
    * Includes retry logic for `yt-dlp` calls to handle transient network issues.
4.  **Transcript Parsing:**
    * Uses the `github.com/asticode/go-astisub` library to parse the downloaded VTT files and extract the plain text content.
5.  **LLM Summarization:**
    * Uses the `github.com/google/generative-ai-go/genai` SDK to send the transcript text to the configured Gemini model.
    * A specific prompt (e.g., asking for a 15-word summary) is used.
    * Includes a timeout for LLM API calls.
6.  **Concurrency:**
    * Processes multiple videos simultaneously using goroutines to improve overall performance.
    * A semaphore limits the number of concurrent operations to avoid overwhelming system resources or API rate limits.
    * Results from concurrent operations are collected using channels.
7.  **Output:**
    * Logs detailed operational messages to standard output (or standard error for logs).
    * Prints a final list of all videos with their fetched summaries or error statuses.
8.  **Cleanup:** Removes temporary transcript files after processing.

## Project Structure (Single File)

Currently, all Go code resides in `main.go`. For larger projects, this would be broken into multiple packages and files.

* **Constants and Configuration:** `AppConfig` struct and related constants.
* **Data Structures:** `VideoDetails`, `ProcessingResult`.
* **Initialization Functions:** `loadEnvironmentFile`, `initializeAppConfig`, `newYouTubeService`, `newGeminiModel`.
* **Core Logic Functions:**
    * `WorkspacePlaylistVideoDetails`: Fetches video metadata.
    * `attemptTranscriptDownload`: Runs `yt-dlp`.
    * `parseDownloadedTranscriptFile`: Parses VTT.
    * `WorkspaceTranscriptWithRetries`: Orchestrates transcript download with retries.
    * `generateSummary`: Calls Gemini API.
    * `processVideoWorker`: Handles the full processing pipeline for a single video.
* **Main Application Flow:** `main`, `runApplication`, `processVideosConcurrently`, `logConfiguration`, `printResults`, `cleanupTempDir`.

## Logging

The application uses the standard `log` package. Log messages include timestamps and provide information about:
* Configuration loading.
* API client initialization.
* Playlist fetching progress.
* Transcript fetching attempts and successes/failures (including `yt-dlp` output on error).
* Summarization attempts and successes/failures.
* Overall processing statistics.

## Future Enhancements / To-Do

* More sophisticated command-line argument parsing (e.g., using the `flag` package to specify playlist ID, concurrency limit, etc., directly on the command line).
* Option to output summaries to a file (CSV, JSON, text).
* Support for different LLM providers.
* More configurable prompt engineering for summaries.
* Better error categorization and reporting.
* Option to specify preferred subtitle languages.
* Unit and integration tests.
* Packaging as a distributable binary for different operating systems.

## Troubleshooting

* **`YOUTUBE_API_KEY` or `GEMINI_API_KEY` not set:** Ensure these are correctly set in your `.env` file or as system environment variables.
* **`yt-dlp