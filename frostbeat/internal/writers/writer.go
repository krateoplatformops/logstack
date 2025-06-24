package writers

type LogWriter interface {
	WriteBatch(batch []string) error
}
