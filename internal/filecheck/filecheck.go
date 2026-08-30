// Package filecheck сверяет записи медиаэлементов с содержимым диска.
//
// Этой сверкой пользуются двое: периодический FileChecker и операция reindex.
// Раньше цикл был скопирован в оба места и они могли разойтись при правках.
package filecheck

import (
	"context"
	"os"

	"github.com/dr-duke/talmorGo/internal/model"
)

// Store — часть репозитория элементов, нужная для сверки.
type Store interface {
	ListAll(ctx context.Context) ([]*model.Item, error)
	MarkLost(ctx context.Context, id string) error
	MarkFound(ctx context.Context, id string) error
}

// Run проверяет наличие файла каждого элемента и обновляет пометку «потерян».
// Удалённые элементы пропускаются: их файла на диске быть и не должно.
// Возвращает число элементов, помеченных потерянными и найденными заново.
func Run(ctx context.Context, store Store) (lost, found int, err error) {
	items, err := store.ListAll(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return lost, found, ctx.Err()
		}
		if item.IsDeleted() {
			continue
		}
		_, statErr := os.Stat(item.Path)
		missing := os.IsNotExist(statErr)

		switch {
		case missing && !item.IsLost():
			if e := store.MarkLost(ctx, item.ID); e == nil {
				lost++
			}
		case !missing && item.IsLost():
			if e := store.MarkFound(ctx, item.ID); e == nil {
				found++
			}
		}
	}
	return lost, found, nil
}
