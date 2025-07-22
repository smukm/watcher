package processing

type ProcessFile interface {
	Execute(filePath string)
}
