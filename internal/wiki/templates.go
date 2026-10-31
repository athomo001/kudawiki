package wiki

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/athomo001/kudawiki/internal/audit"
	"github.com/athomo001/kudawiki/internal/repo"
)

// ErrInvalidTemplate: plantilla sin nombre, con un campo demasiado largo o con un nivel que
// quien la guarda no tiene (§4.9).
var ErrInvalidTemplate = errors.New("invalid_template")

const (
	maxTemplateName        = 80
	maxTemplateDescription = 300
)

type templateStore interface {
	TemplateByID(ctx context.Context, s repo.Scope, id string) (repo.Template, error)
	SaveTemplate(ctx context.Context, s repo.Scope, actorID string, t repo.Template) (string, error)
	DeleteTemplate(ctx context.Context, s repo.Scope, id string) error
}

// SaveTemplate crea (id vacío) o reemplaza una plantilla del área del Scope, o una global con
// repo.GlobalScope. La auditoría guarda el id, nunca el contenido.
func (s *Service) SaveTemplate(ctx context.Context, sc repo.Scope, actorID string, t repo.Template) (string, error) {
	// Primero si existe: una que no existe (o que no ve) es el 404 uniforme, aunque venga mal (§3.3).
	if t.ID != "" {
		if _, err := s.store.TemplateByID(ctx, sc, t.ID); err != nil {
			return "", err
		}
	}
	t.Name, t.Description, t.Title = strings.TrimSpace(t.Name), strings.TrimSpace(t.Description), strings.TrimSpace(t.Title)
	if t.Name == "" || utf8.RuneCountInString(t.Name) > maxTemplateName ||
		utf8.RuneCountInString(t.Description) > maxTemplateDescription ||
		utf8.RuneCountInString(t.Title) > maxTitle || len(t.Markdown) > maxMarkdown ||
		t.MinRank < 1 || t.MinRank > sc.MaxRank || !validReviewDays(t.ReviewDays) {
		return "", ErrInvalidTemplate
	}
	// El texto oculto mal cerrado se publicaría a la vista en cada página que nazca de ella (§4.10).
	if strings.Contains(t.Markdown, "secret") {
		if err := checkSecrets(t.Markdown); err != nil {
			return "", err
		}
	}
	tags, err := CleanTags(t.Tags)
	if err != nil {
		return "", err
	}
	t.Tags = tags
	action := audit.TemplateUpdate
	if t.ID == "" {
		action = audit.TemplateCreate
	}
	id, err := s.store.SaveTemplate(ctx, sc, actorID, t)
	if err != nil {
		return "", err
	}
	s.audit.Record(ctx, audit.Event{Action: action, WsID: sc.WsID, TargetType: "template", TargetID: id})
	return id, nil
}

func (s *Service) DeleteTemplate(ctx context.Context, sc repo.Scope, id string) error {
	if err := s.store.DeleteTemplate(ctx, sc, id); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Event{Action: audit.TemplateDelete, WsID: sc.WsID, TargetType: "template", TargetID: id})
	return nil
}

// UseTemplate llena una plantilla para empezar una página nueva (§4.9): {{fecha}}, {{autor}} y
// {{titulo}}. El título es el que escribió quien crea la página; si no escribió ninguno, el de la
// plantilla, con sus propias variables ya llenas. Lo que devuelve va al editor: nada se guarda
// hasta que se publica o se propone.
func (s *Service) UseTemplate(ctx context.Context, sc repo.Scope, id, title, author string, now time.Time) (repo.Template, error) {
	t, err := s.store.TemplateByID(ctx, sc, id)
	if err != nil {
		return repo.Template{}, err
	}
	date := now.Format(time.DateOnly)
	fill := strings.NewReplacer("{{fecha}}", date, "{{autor}}", author)
	if title = strings.TrimSpace(title); title == "" {
		title = fill.Replace(t.Title)
	}
	t.Title = title
	t.Markdown = strings.NewReplacer("{{fecha}}", date, "{{autor}}", author, "{{titulo}}", title).Replace(t.Markdown)
	return t, nil
}
