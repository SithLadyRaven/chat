package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"os"
	"regexp"
	"strconv"
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
	Chat struct {
		APIKey   string `mapstructure:"api_key"`
		UserInfo string `mapstructure:"user_info"`
	} `mapstructure:"chat"`
}

func loadConfig(configFile string) (Config, error) {
	var config Config
	viper.AddConfigPath(".")
	viper.SetConfigName(configFile)
	viper.SetConfigType("yaml")
	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		return config, err
	}

	err = viper.Unmarshal(&config)
	return config, err
}

func summarizeConversation(client *openai.Client, ctx context.Context, messages []openai.ChatCompletionMessage) (string, error) {
	summaryRequest := openai.ChatCompletionRequest{
		Model: openai.GPT4oLatest,
		Messages: append(messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: "Please provide a summary of the conversation so far.",
		}),
	}

	response, err := client.CreateChatCompletion(ctx, summaryRequest)
	if err != nil {
		return "", err
	}

	return response.Choices[0].Message.Content, nil
}

func saveConversation(filename string, conversation []openai.ChatCompletionMessage, client *openai.Client, ctx context.Context) error {
	if filename == "" {
		conversations, err := listConversations()
		if err != nil {
			return err
		}
		titleRequest := openai.ChatCompletionRequest{
			Model: openai.GPT4oLatest,
			Messages: append(conversation, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleUser,
				Content: fmt.Sprintf("Provide a title for this chat conversation. Try to keep it under 50 characters in length. Include only the title in your response with no other text before or after the title. Use \"_\" rather than spaces. Avoid using any of the following titles: %s", strings.Join(conversations, ", ")),
			}),
		}

		response, err := client.CreateChatCompletion(ctx, titleRequest)
		if err != nil {
			return err
		}

		filename = response.Choices[0].Message.Content
	}

	file, err := os.Create("./conversations/" + filename + ".json")
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(conversation)
	if err != nil {
		return err
	}
	fmt.Println("Conversation saved to", "./conversations/"+filename+".json")
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

func attachFile(filename string) (string, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}

	mimeType := mime.TypeByExtension(filename)
	switch mimeType {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document", // .docx
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": // .xlsx
		// Encode the binary data to base64
		encoded := base64.StdEncoding.EncodeToString(data)
		// Return a formatted message indicating the type and encoded content
		return fmt.Sprintf("[FILE %s]", encoded), nil
	default:
		// Assuming it's safe plaintext if mime type not specified (can add more cases)
		return string(data), nil
	}
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

func readImageAsBase64(filename string) (string, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", err
	}

	encoded := base64.StdEncoding.EncodeToString(data)
	return encoded, nil
}

func startLoadingIndicator(indicatorRunning *bool, indicatorDone chan bool) {
	if !*indicatorRunning {
		go loadingIndicator(indicatorDone)
		*indicatorRunning = true
	}
}

func listConversations() ([]string, error) {
	files, err := ioutil.ReadDir("./conversations")
	if err != nil {
		return nil, err
	}

	var conversations []string
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".json") {
			conversations = append(conversations, strings.TrimSuffix(file.Name(), ".json"))
		}
	}

	return conversations, nil
}

func analyzeImage(client *openai.Client, ctx context.Context, image string, messages []openai.ChatCompletionMessage) (openai.ChatCompletionMessage, error) {
	request := openai.ChatCompletionRequest{
		Model:    openai.GPT4oLatest,
		Messages: messages,
	}

	imageBase64, _ := readImageAsBase64(image)
	var imgMessage = openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser,
		MultiContent: []openai.ChatMessagePart{
			{
				Type: openai.ChatMessagePartTypeText,
				Text: "Here is the photo for your analysis. Be sure to provide a thorough and detailed analysis to ensure that you have context for everything regarding it if it comes up again in a future conversation. This is for context only and the user will note see this message. Give a clinical analysis with out responding as the user directed but do assume the person in the image is Kelsey. Also include whetehr you would with no other context, gender her as male or female. Also include whether the facial aspects appear feminine or masculine",
			},
			{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL:    "data:image/jpeg;base64," + imageBase64,
					Detail: openai.ImageURLDetailHigh,
				},
			},
		},
	}

	fmt.Println("Analyze Request sent")
	request.Messages = append(request.Messages, imgMessage)
	response, _ := client.CreateChatCompletion(ctx, request)
	fmt.Println("Analysis response: " + response.Choices[0].Message.Content)

	return openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: response.Choices[0].Message.Content,
	}, nil
}

func main() {
	configFile := flag.String("config", "config.yaml", "Specify the config file")
	configFileShort := flag.String("c", "config.yaml", "Specify the config file (short flag)")

	format := flag.Bool("format", false, "Enable formatting")
	flag.BoolVar(format, "f", false, "Enable formatting (short flag)")
	flag.Parse()

	config, err := loadConfig(*configFile)
	if err != nil {
		config, err = loadConfig(*configFileShort)
		if err != nil {
			log.Fatalf("Error loading config: %v", err)
		}
	}

	godotenv.Load()

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		apiKey = config.Chat.APIKey

		if apiKey == "" {
			log.Fatalln("Missing API KEY")
		}
	}

	//f, err := os.Create(*outputFile)
	//if err != nil {
	//	fmt.Println("error opening file: err:", err)
	//	os.Exit(1)
	//}

	client := openai.NewClient(apiKey)
	ctx := context.Background()

	var request = openai.ChatCompletionRequest{
		Model:  openai.GPT4oLatest,
		Stream: true,
	}

	// Add initial system message with user info
	initialMessage := openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: config.Chat.UserInfo,
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
		blankLineCount := 0
	getInput:
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				fmt.Println("Error reading input:", err)
				break getInput
			}
			line = strings.TrimRight(line, "\n")
			line = strings.TrimRight(line, "\r")

			if line == "" {
				blankLineCount++
				if blankLineCount >= 2 {
					break getInput
				}
				continue getInput
			} else {
				blankLineCount = 0
			}

			if strings.HasPrefix(line, "\\analyze") {
				parts := strings.Fields(line)
				if len(parts) < 2 {
					fmt.Println("Please provide a filename for the image to be analyzed")
					continue getInput
				}

				analysisMsg, _ := analyzeImage(client, ctx, parts[1], request.Messages)

				request.Messages = append(request.Messages, analysisMsg)

				continue getInput

			} else if strings.HasPrefix(line, "\\img") {
				parts := strings.Fields(line)
				if len(parts) < 2 {
					fmt.Println("Please provide a filename for the image.")
					continue getInput
				}
				imageBase64, imgErr := readImageAsBase64(parts[1])
				if imgErr != nil {
					fmt.Println("Error reading image:", imgErr)
					continue getInput
				}
				var imgMessage = openai.ChatCompletionMessage{
					Role: openai.ChatMessageRoleUser,
					MultiContent: []openai.ChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: "Photo attached by the user",
						},
						{
							Type: openai.ChatMessagePartTypeImageURL,
							ImageURL: &openai.ChatMessageImageURL{
								URL:    "data:image/jpeg;base64," + imageBase64,
								Detail: openai.ImageURLDetailHigh,
							},
						},
					},
				}
				request.Messages = append(request.Messages, imgMessage)

				fmt.Printf("Image %s included as base64.\n", parts[1])
				fmt.Println("")
				continue getInput
			} else {
				if strings.HasPrefix(line, "\\save") {
					parts := strings.Fields(line)
					filename := ""
					if len(parts) == 2 {
						filename = parts[1]
					}
					saveErr := saveConversation(filename, request.Messages, client, ctx)
					if saveErr != nil {
						fmt.Println("Error saving conversation:", saveErr)
					}
					continue getInput
				}
				if strings.HasPrefix(line, "\\load") {
					conversations, err := listConversations()
					if err != nil {
						fmt.Println("Error listing conversations:", err)
						continue getInput
					}

					fmt.Println("Select a conversation to load:")
					for i, conversation := range conversations {
						fmt.Printf("%d: %s\n", i+1, conversation)
					}

					fmt.Print("Enter the number of the conversation to load: ")
					line, err = reader.ReadString('\n')
					if err != nil {
						fmt.Println("Error reading input:", err)
						continue getInput
					}

					line = strings.TrimSpace(line)
					choice, err := strconv.Atoi(line)
					if err != nil || choice < 1 || choice > len(conversations) {
						fmt.Println("Invalid choice. Please enter a valid number.")
						continue getInput
					}

					request.Messages, err = loadConversation("./conversations/" + conversations[choice-1] + ".json")
					if err != nil {
						fmt.Println("Error loading conversation:", err)
					}
					continue getInput
				}
				if strings.HasPrefix(line, "\\list") {
					conversations, err := listConversations()
					if err != nil {
						fmt.Println("Error listing conversations:", err)
					} else {
						fmt.Println("Saved conversations:")
						for _, conversation := range conversations {
							fmt.Println(conversation)
						}
					}
					continue getInput
				}
				if strings.EqualFold(line, "\\done") || strings.EqualFold(line, "\\d") || strings.EqualFold(line, "/d") {
					break getInput
				}
				if strings.HasPrefix(line, "\\summarize") {
					summary, err := summarizeConversation(client, ctx, request.Messages)
					if err != nil {
						fmt.Println("Error summarizing conversation:", err)
						continue getInput
					}

					request.Messages = []openai.ChatCompletionMessage{
						initialMessage,
						{
							Role:    openai.ChatMessageRoleSystem,
							Content: summary,
						},
					}
					fmt.Println("Conversation summarized and reset.")
					continue getInput
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
				inputLines = append(inputLines, line)
			}
		}

		if len(inputLines) == 0 {
			continue
		}
		fmt.Printf("You: %s\n", inputLines[0])

		for _, input := range inputLines[1:] {
			fmt.Println(input)
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
		request.Messages = append(request.Messages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: msg,
		})
	}

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
