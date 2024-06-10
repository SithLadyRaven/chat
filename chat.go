package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	markdown "github.com/MichaelMure/go-term-markdown"
	"github.com/joho/godotenv"
	openai "github.com/sashabaranov/go-openai"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh/terminal"
	//	"github.com/chzyer/readline"
)

type Config struct {
	APIKey   string `mapstructure:"API_KEY"`
	UserInfo string `mapstructure:"USER_INFO"`
}

func loadConfig() (Config, error) {
	var config Config
	viper.AddConfigPath(".")
	viper.SetConfigName("config")
	viper.SetConfigType("env")
	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		return config, err
	}

	err = viper.Unmarshal(&config)
	return config, err
}

func saveConversation(filename string, conversation []openai.ChatCompletionMessage) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(conversation)
	if err != nil {
		return err
	}

	fmt.Println("Conversation saved to", filename)
	return nil
}

func loadConversation(filename string) ([]openai.ChatCompletionMessage, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var conversation []openai.ChatCompletionMessage
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&conversation)
	if err != nil {
		return nil, err
	}

	fmt.Println("Conversation loaded from", filename)
	return conversation, nil
}

func isExitString(userInput string) bool {
	lowerInput := strings.ToLower(userInput)
	if lowerInput == "q" || lowerInput == "exit" || lowerInput == "quit" || lowerInput == "e" || lowerInput == "\\q" || lowerInput == "\\quit" {
		return true
	}
	return false
}

func detectListItem(part string) bool {
	isListItem := regexp.MustCompile(`^\s*[\d+\.\:\)-]+\s+|^\s*[*+-]\s+`)
	return isListItem.MatchString(part)
}

// detectContinuation checks if the line is a continuation of the previous line.
func detectContinuation(part string) bool {
	continuation := regexp.MustCompile(`^\s+`)
	return continuation.MatchString(part)
}

func detectCodeBlockStartEnd(part string) bool {
	codeBlock := regexp.MustCompile("^\\s*```")
	return codeBlock.MatchString(part)
}

func renderMarkdown(content string) string {
	width, _, _ := terminal.GetSize(int(os.Stdout.Fd()))
	return string(markdown.Render(content, width, 4))
}

func detectTableRow(part string) bool {
	// Detect if the string appears to be a Markdown table row
	tableRow := regexp.MustCompile(`^\|.*\|$`)
	return tableRow.MatchString(part)
}

func startLoadingIndicator(indicatorRunning *bool, indicatorDone chan bool) {
	if !*indicatorRunning {
		go loadingIndicator(indicatorDone)
		*indicatorRunning = true
	}
}

func main() {
	config, err := loadConfig()
	godotenv.Load()
	format := flag.Bool("format", false, "Enable formatting")
	flag.BoolVar(format, "f", false, "Enable formatting (short flag)")
	flag.Parse()

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		apiKey = config.APIKey

		if apiKey == "" {
			log.Fatalln("Missing API KEY")
		}
	}

	t := time.Now()
	timestamp := t.Format("060102150405")
	filename := "conversation_" + timestamp + ".txt"

	f, err := os.Create(filename)
	if err != nil {
		fmt.Println("error opening file: err:", err)
		os.Exit(1)
	}

	client := openai.NewClient(apiKey)
	ctx := context.Background()

	var request = openai.ChatCompletionRequest{
		Model:  openai.GPT4o,
		Stream: true,
	}

	// Add initial system message with user info
	initialMessage := openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: config.UserInfo,
	}
	request.Messages = append(request.Messages, initialMessage)

	var newMessage = openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser,
	}

mainLoop:
	for {
		fmt.Print("Message:")
		reader := bufio.NewReader(os.Stdin)

		var inputLines []string
	getInput:
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				fmt.Println("Error reading input:", err)
				break getInput
			}
			line = strings.TrimRight(line, "\n")
			line = strings.TrimRight(line, "\r")

			if strings.HasPrefix(line, "\\save") {
				parts := strings.Fields(line)
				if len(parts) < 2 {
					fmt.Println("Please provide a filename to save the conversation.")
					continue getInput
				}
				saveErr := saveConversation(parts[1], request.Messages)
				if saveErr != nil {
					fmt.Println("Error saving conversation:", saveErr)
				}
				continue getInput
			}
			if strings.HasPrefix(line, "\\load") {
				parts := strings.Fields(line)
				if len(parts) < 2 {
					fmt.Println("Please provide a filename to load the conversation.")
					continue getInput
				}
				request.Messages, err = loadConversation(parts[1])
				if err != nil {
					fmt.Println("Error loading conversation:", err)
				}
				continue getInput
			}
			if strings.EqualFold(line, "\\done") || strings.EqualFold(line, "\\d") || strings.EqualFold(line, "/d") {
				break getInput
			}
			if strings.HasSuffix(line, "\\done") {
				inputLines = append(inputLines, strings.TrimSuffix(line, "\\done"))
				break getInput
			} else if strings.HasSuffix(line, "\\d") {
				inputLines = append(inputLines, strings.TrimSuffix(line, "\\d"))
				break getInput
			} else if strings.HasSuffix(line, "/d") {
				inputLines = append(inputLines, strings.TrimSuffix(line, "/d"))
				break getInput
			}
			if len(inputLines) == 0 {
				if isExitString(line) {
					break mainLoop
				}
			}
			fmt.Println(line)
			inputLines = append(inputLines, line)
		}

		if len(inputLines) == 0 {
			continue
		}
		fmt.Fprintf(f, "You: %s\n", inputLines[0])
		fmt.Printf("You: %s\n", inputLines[0])

		for _, input := range inputLines[1:] {
			fmt.Println(input)
			fmt.Fprintln(f, input)
		}

		newMessage.Content = strings.Join(inputLines, " ")
		request.Messages = append(request.Messages, newMessage)

		stream, err := client.CreateChatCompletionStream(
			ctx,
			request,
		)

		if err != nil {
			return
		}
		defer stream.Close()

		fmt.Println("\nChatGPT: ")
		f.WriteString("\nChatGPT: ")
		msg := ""
		buffer := ""
		partialBuffer := ""
		inList := false
		lineAccum := ""
		inCodeBlock := false
		inTable := false

		indicatorDone := make(chan bool)
		indicatorRunning := false

		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				fmt.Printf("\nStream error: %v\n", err)
				break
			}

			msg += resp.Choices[0].Delta.Content
			f.WriteString(resp.Choices[0].Delta.Content)

			if *format {
				partialBuffer += resp.Choices[0].Delta.Content

				// Check if the buffer contains a newline character
				if strings.Contains(partialBuffer, "\n") {
					parts := strings.Split(partialBuffer, "\n")
					for _, part := range parts {
						if part == "" {
							continue
						}
						lineAccum += part

						if detectCodeBlockStartEnd(lineAccum) {
							startLoadingIndicator(&indicatorRunning, indicatorDone)

							if inCodeBlock {
								buffer += "\n" + lineAccum + "\n"
								fmt.Println(renderMarkdown(buffer))
								buffer = ""
								inCodeBlock = false
								indicatorDone <- true
								indicatorRunning = false
							} else {
								inCodeBlock = true
								buffer += "\n" + lineAccum + "\n"
							}
							lineAccum = ""
						} else if inCodeBlock {
							buffer += lineAccum + "\n"
							lineAccum = ""
						} else if detectListItem(lineAccum) || (inList && detectContinuation(lineAccum)) {
							startLoadingIndicator(&indicatorRunning, indicatorDone)
							buffer += lineAccum + "\n"
							inList = true
							lineAccum = ""
						} else if detectTableRow(lineAccum) {
							startLoadingIndicator(&indicatorRunning, indicatorDone)
							buffer += lineAccum + "\n"
							inTable = true
							lineAccum = ""
						} else {
							if inTable {
								fmt.Println(renderMarkdown(buffer))
								buffer = ""
								inTable = false
								indicatorDone <- true
								indicatorRunning = false
							}
							if inList {
								fmt.Println(renderMarkdown(buffer))
								buffer = ""
								inList = false
								indicatorDone <- true
								indicatorRunning = false
							}
							if lineAccum != "" {
								fmt.Println(renderMarkdown(lineAccum))
							}
							lineAccum = ""
						}
					}
					partialBuffer = ""
				}
			} else {
				fmt.Print(resp.Choices[0].Delta.Content)
			}
		}

		if indicatorRunning {
			indicatorDone <- true
		}

		// Flush any remaining accumulated line
		if lineAccum != "" {
			if detectTableRow(lineAccum) || detectCodeBlockStartEnd(lineAccum) || detectListItem(lineAccum) || (inList && detectContinuation(lineAccum)) {
				buffer += "\n" + lineAccum + "\n"
			} else {
				fmt.Print(renderMarkdown(lineAccum))
			}
		}

		// Check for leftover buffer to flush at the end, especially for lists.
		if partialBuffer != "" || buffer != "" {
			buffer += "\n" + partialBuffer
			fmt.Print(renderMarkdown(buffer))
		}

		fmt.Println("\n")
		f.WriteString("\n")
		request.Messages = append(request.Messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: msg,
		})
	}

	defer f.Close()
}

func showIndicator(showing bool) bool {
	return !showing
}

func loadingIndicator(done chan bool) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	states := []string{".  ", ".. ", "..."}
	stateIdx := 0

	for {
		select {
		case <-done:
			// Clear the loading indicator
			fmt.Print("\r    \r")
			fmt.Print("\r3")

			return
		case <-ticker.C:
			fmt.Printf("\r%s", states[stateIdx])
			stateIdx = (stateIdx + 1) % len(states)
		}
	}
}
