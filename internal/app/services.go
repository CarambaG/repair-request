package app

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const maxRequestServiceQuantity = 99

func parseRequestServices(form url.Values, catalog []ServiceOption) ([]RequestServiceItem, map[string]string, error) {
	quantities := make(map[string]string, len(catalog))
	items := make([]RequestServiceItem, 0, len(catalog))

	for _, service := range catalog {
		fieldName := requestServiceFieldName(service.Code)
		rawQuantity := strings.TrimSpace(form.Get(fieldName))
		quantities[service.Code] = rawQuantity

		if rawQuantity == "" {
			continue
		}

		quantity, err := strconv.Atoi(rawQuantity)
		if err != nil {
			return nil, quantities, fmt.Errorf("количество для услуги «%s» указано некорректно", service.Name)
		}
		if quantity < 0 {
			return nil, quantities, fmt.Errorf("количество для услуги «%s» не может быть отрицательным", service.Name)
		}
		if quantity > maxRequestServiceQuantity {
			return nil, quantities, fmt.Errorf("количество для услуги «%s» не должно превышать %d", service.Name, maxRequestServiceQuantity)
		}
		if quantity == 0 {
			continue
		}

		items = append(items, RequestServiceItem{
			ServiceCode: service.Code,
			ServiceName: service.Name,
			Quantity:    quantity,
		})
	}

	if len(catalog) == 0 {
		return nil, quantities, fmt.Errorf("список услуг недоступен")
	}
	if len(items) == 0 {
		return nil, quantities, fmt.Errorf("выберите хотя бы одну услугу")
	}

	return items, quantities, nil
}

func insertRequestServices(ctx context.Context, tx pgx.Tx, requestID int64, items []RequestServiceItem) error {
	for _, item := range items {
		_, err := tx.Exec(ctx, `
			INSERT INTO request_services (request_id, service_code, service_name, quantity)
			VALUES ($1, $2, $3, $4)
		`, requestID, item.ServiceCode, item.ServiceName, item.Quantity)
		if err != nil {
			return err
		}
	}
	return nil
}

func requestServiceFieldName(code string) string {
	return "service_qty_" + code
}

func serviceQtyValue(values map[string]string, code string) string {
	if values == nil {
		return ""
	}
	return values[code]
}
