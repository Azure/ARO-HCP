package frontend

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Azure/ARO-HCP/internal/api"
	"github.com/Azure/ARO-HCP/internal/api/arm"
)

type ContextError struct {
	got any
}

func (c *ContextError) Error() string {
	return fmt.Sprintf(
		"error retrieving value from context, value obtained was '%v' and type obtained was '%T'",
		c.got,
		c.got)
}

type contextKey int

const (
	// Keys for request-scoped data in http.Request contexts
	contextKeyOriginalPath contextKey = iota
	contextKeyBody
	contextKeyLogger
	contextKeyVersion
	contextKeyResourceID
	contextKeyCorrelationData
	contextKeySystemData
	contextKeySubscription
)

func ContextWithOriginalPath(ctx context.Context, originalPath string) context.Context {
	return context.WithValue(ctx, contextKeyOriginalPath, originalPath)
}

func OriginalPathFromContext(ctx context.Context) (string, error) {
	originalPath, ok := ctx.Value(contextKeyOriginalPath).(string)
	if !ok {
		err := &ContextError{
			got: originalPath,
		}
		return originalPath, err
	}
	return originalPath, nil
}

func ContextWithBody(ctx context.Context, body []byte) context.Context {
	return context.WithValue(ctx, contextKeyBody, body)
}

func BodyFromContext(ctx context.Context) ([]byte, error) {
	body, ok := ctx.Value(contextKeyBody).([]byte)
	if !ok {
		err := &ContextError{
			got: body,
		}
		return body, err
	}
	return body, nil
}

func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKeyLogger, logger)
}

func LoggerFromContext(ctx context.Context) (*slog.Logger, error) {
	logger, ok := ctx.Value(contextKeyLogger).(*slog.Logger)
	if !ok {
		err := &ContextError{
			got: logger,
		}
		return logger, err
	}
	return logger, nil
}

func ContextWithVersion(ctx context.Context, version api.Version) context.Context {
	return context.WithValue(ctx, contextKeyVersion, version)
}

func VersionFromContext(ctx context.Context) (api.Version, error) {
	version, ok := ctx.Value(contextKeyVersion).(api.Version)
	if !ok {
		err := &ContextError{
			got: version,
		}
		return version, err
	}
	return version, nil
}

func ContextWithResourceID(ctx context.Context, resourceID *arm.ResourceID) context.Context {
	return context.WithValue(ctx, contextKeyResourceID, resourceID)
}

func ResourceIDFromContext(ctx context.Context) (*arm.ResourceID, error) {
	resourceID, ok := ctx.Value(contextKeyResourceID).(*arm.ResourceID)
	if !ok {
		err := &ContextError{
			got: resourceID,
		}
		return resourceID, err
	}
	return resourceID, nil
}

func ContextWithCorrelationData(ctx context.Context, correlationData *arm.CorrelationData) context.Context {
	return context.WithValue(ctx, contextKeyCorrelationData, correlationData)
}

func CorrelationDataFromContext(ctx context.Context) (*arm.CorrelationData, error) {
	correlationData, ok := ctx.Value(contextKeyCorrelationData).(*arm.CorrelationData)
	if !ok {
		err := &ContextError{
			got: correlationData,
		}
		return correlationData, err
	}
	return correlationData, nil
}

func ContextWithSystemData(ctx context.Context, systemData *arm.SystemData) context.Context {
	return context.WithValue(ctx, contextKeySystemData, systemData)
}

func SystemDataFromContext(ctx context.Context) (*arm.SystemData, error) {
	systemData, ok := ctx.Value(contextKeySystemData).(*arm.SystemData)
	if !ok {
		err := &ContextError{
			got: systemData,
		}
		return systemData, err
	}
	return systemData, nil
}

func ContextWithSubscription(ctx context.Context, subscription arm.Subscription) context.Context {
	return context.WithValue(ctx, contextKeySubscription, subscription)
}

func SubscriptionFromContext(ctx context.Context) (arm.Subscription, error) {
	sub, ok := ctx.Value(contextKeySubscription).(arm.Subscription)
	if !ok {
		return arm.Subscription{}, &ContextError{
			got: sub,
		}
	}
	return sub, nil
}
