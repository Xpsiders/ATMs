package httpx

import "github.com/go-playground/validator/v10"

var requestValidator = validator.New()

func ValidateStruct(value any) error {
	return requestValidator.Struct(value)
}
