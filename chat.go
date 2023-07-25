package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
	openai "github.com/sashabaranov/go-openai"
)

func isExitString(userInput string) bool {
	lowerInput := strings.ToLower(userInput)
	if lowerInput == "q" || lowerInput == "exit" || lowerInput == "quit" || lowerInput == "e" || lowerInput == "\\q" || lowerInput == "\\quit" {
		return true
	}
	return false
}

func main() {
	godotenv.Load()

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		log.Fatalln("Missing API KEY")
	}

	t := time.Now()
	timestamp := t.Format("060102150405")
	filename := "conversation_" + timestamp + ".txt"

	f, err := os.Create(filename)
	if err != nil {
		log.Fatalln("error opening file: err:", err)
		os.Exit(1)
	}

	client := openai.NewClient(apiKey)

	var request = openai.ChatCompletionRequest{
		Model: openai.GPT3Dot5Turbo,
	}

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
			if strings.EqualFold(line, "\\done") || strings.EqualFold(line, "\\d") {
				break getInput
			}
			if strings.HasSuffix(line, "\\done") {
				inputLines = append(inputLines, strings.TrimSuffix(line, "\\done"))
				break getInput
			} else if strings.HasSuffix(line, "\\d") {
				inputLines = append(inputLines, strings.TrimSuffix(line, "\\d"))
				break getInput
			}
			if len(inputLines) == 0 {
				if isExitString(line) {
					break mainLoop
				}
			}
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

		resp, err := client.CreateChatCompletion(
			context.Background(),
			request,
		)

		if err != nil {
			return
		}

		fmt.Println("\nChatGPT: " + resp.Choices[0].Message.Content)
		fmt.Println("\n")
		f.WriteString("\nChatGPT: " + resp.Choices[0].Message.Content + "\n")

		request.Messages = append(request.Messages, resp.Choices[0].Message)

	}

	defer f.Close()
}
