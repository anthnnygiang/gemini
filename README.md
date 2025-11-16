# LLM

Custom Go TUI implementation for LLM chat.
For Google Gemini API.

## Usage

1. Set environment variable `GOOGLE_CLI` as your Google Gemini API key.
2. `go install` to install the binary to Go bin path.
3. Run `llm`.

## Note

- While streaming a response, any new prompt will stop the current response and start a new one.

## TODO:

- Keybind to select previous message
- Clear history
