package main

// A simple program demonstrating the text area component from the Bubbles
// component library.

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/genai"
)

const GAP = "\n\n"
const SYSTEM_INSTRUCTION = "answer concisely."
const MODEL = "gemini-2.5-flash"
const GOOGLE_CLI = "GOOGLE_CLI"

type errMsg error

type streamMsg struct {
	index int
	part  string
}

type model struct {
	viewport    viewport.Model
	history     []*genai.Content
	display     []string // TODO: derive display from history
	textarea    textarea.Model
	promptStyle lipgloss.Style
	chatStyle   lipgloss.Style
	chat        *genai.Chat
	next        tea.Cmd
	ctx         context.Context
	err         error
	log         *os.File
}

func main() {
	// clear file
	os.WriteFile("debug.log", []byte{}, 0644)
	f, err := tea.LogToFile("debug.log", "debug")
	if err != nil {
		fmt.Println("fatal:", err)
		os.Exit(1)
	}
	defer f.Close()
	m := initialModel(f)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
func initialModel(f *os.File) tea.Model {
	GEMINI_API_KEY := os.Getenv(GOOGLE_CLI)
	if GEMINI_API_KEY == "" {
		log.Fatal("missing 'GOOGLE_CLI' env variable.")
	}
	ctx := context.Background()
	clientConfig := genai.ClientConfig{
		APIKey: GEMINI_API_KEY,
	}
	client, err := genai.NewClient(ctx, &clientConfig)
	if err != nil {
		log.Fatal(err)
	}
	displayHistory := []string{}
	chatHistory := []*genai.Content{}
	chatConfig := genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(SYSTEM_INSTRUCTION, genai.RoleModel),
	}
	chatClient, _ := client.Chats.Create(ctx, MODEL, &chatConfig, chatHistory)

	ta := textarea.New()
	ta.Placeholder = "prompt.."
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 0 // no limit
	ta.SetWidth(40)
	ta.SetHeight(2)

	// Remove cursor line styling
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.ShowLineNumbers = false

	vp := viewport.New(40, 5)
	vp.KeyMap = viewport.KeyMap{
		HalfPageUp: key.NewBinding(
			key.WithKeys("ctrl+u"),
		),
		HalfPageDown: key.NewBinding(
			key.WithKeys("ctrl+d"),
		)}

	ta.KeyMap.InsertNewline.SetEnabled(false)
	ta.KeyMap.LineNext.SetEnabled(false) // TODO: change up/down keypress to navigate history
	ta.KeyMap.LinePrevious.SetEnabled(false)

	return model{
		textarea:    ta,
		history:     chatHistory,
		display:     displayHistory,
		viewport:    vp,
		promptStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
		chatStyle:   lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		chat:        chatClient,
		ctx:         ctx,
		err:         nil,
		next:        nil,
		log:         f,
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var tiCmd tea.Cmd
	var vpCmd tea.Cmd
	// update textarea and viewport components
	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	// handle messages
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.windowSizeMsg(msg)

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			return m.keyEsc()
		case tea.KeyEnter:
			return m.keyEnter()
		}

	case streamMsg:
		return m.streamMsg(msg)

	case errMsg:
		return m.errMsg()
	}
	return m, tea.Batch(tiCmd, vpCmd)
}

// View renders the UI of the program
func (m model) View() string {
	return fmt.Sprintf(
		"%s%s%s",
		m.viewport.View(),
		GAP,
		m.textarea.View(),
	)
}

// keyEsc handles the escape key press to exit the program
func (m model) keyEsc() (tea.Model, tea.Cmd) {
	return m, tea.Quit
}

// errMsg handles error messages
func (m model) errMsg() (tea.Model, tea.Cmd) {
	return m, nil
}

// windowSizeMsg adjusts the viewport and textarea sizes based on the window size
func (m *model) windowSizeMsg(msg tea.WindowSizeMsg) {
	m.viewport.Width = msg.Width
	m.textarea.SetWidth(msg.Width)
	m.viewport.Height = msg.Height - m.textarea.Height() - lipgloss.Height(GAP)

	if len(m.display) > 0 {
		m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.display, "\n")))
	}
	m.viewport.GotoBottom()
}

// keyEnter handles the enter key press to send the prompt and stream the response
func (m model) keyEnter() (tea.Model, tea.Cmd) {
	prompt := m.textarea.Value() // capture prompt before reset textarea
	m.history = append(m.history, genai.NewContentFromText(prompt, genai.RoleUser))
	m.display = append(m.display, m.chatStyle.Render("? ")+m.promptStyle.Render(m.textarea.Value()))
	m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.display, "\n"))) // TODO: why NewStyle?

	index := len(m.display) // capture current index
	parts := make(chan string)
	go func() {
		stream := m.chat.SendMessageStream(m.ctx, genai.Part{Text: prompt})
		for chunk, _ := range stream {
			if chunk == nil {
				continue
			}
			part := chunk.Candidates[0].Content.Parts[0].Text
			parts <- part // push parts into channel in order
		}
		close(parts)
	}()

	readPart := func() tea.Msg {
		i := index       // capture current index in closure
		p, ok := <-parts // closure captures parts channel and current index
		if !ok {
			return nil // channel closed
		}
		return streamMsg{index: i, part: p}
	}

	m.textarea.Reset()
	m.viewport.GotoBottom()
	m.next = readPart
	return m, m.next
}

func (m model) streamMsg(msg streamMsg) (tea.Model, tea.Cmd) {
	if msg.index == len(m.display) {
		m.display = append(m.display, m.chatStyle.Render("> ")) // add new entry
	}
	m.display[msg.index] += msg.part
	m.viewport.SetContent(lipgloss.NewStyle().Width(m.viewport.Width).Render(strings.Join(m.display, "\n")))
	m.viewport.GotoBottom()
	if m.next == nil {
		return m, nil // read all parts
	}

	return m, m.next
}
