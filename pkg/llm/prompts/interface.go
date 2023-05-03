package prompts

type PromptTemplate interface {
	String(promptContext map[string]string) (string, error)
}
