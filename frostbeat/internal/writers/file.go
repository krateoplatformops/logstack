package writers

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

func File(path string) (LogWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &fileWriterImpl{File: f}, nil
}

type fileWriterImpl struct {
	File *os.File
}

func (w *fileWriterImpl) WriteBatch(batch []string) error {
	for _, raw := range batch {
		var data map[string]any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			log.Printf("FileWriter: log non valido: %v", err)
			continue
		}

		txID, _ := data["traceId"].(string)
		if txID == "" {
			log.Printf("FileWriter: missing traceId")
			continue
		}

		rawTime, _ := data["time"].(string)
		parsedTime, err := time.Parse(time.RFC3339Nano, rawTime)
		if err != nil {
			log.Printf("FileWriter: invalid time format: %v", err)
			continue
		}

		entry := map[string]any{
			"timestamp": parsedTime.UnixNano(),
			"traceId":   txID,
			"log":       data,
		}

		jsonLine, err := json.Marshal(entry)
		if err != nil {
			log.Printf("FileWriter: errore serializzazione JSON: %v", err)
			continue
		}

		_, err = w.File.Write(append(jsonLine, '\n'))
		if err != nil {
			return err
		}
	}
	return nil
}
