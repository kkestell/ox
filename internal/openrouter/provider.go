package openrouter

func (p *Provider) resolved(hasTools bool) *Provider {
	if p == nil {
		if !hasTools {
			return nil
		}
		required := true
		return &Provider{RequireParameters: &required}
	}

	resolved := *p
	if p.RequireParameters == nil && hasTools {
		required := true
		resolved.RequireParameters = &required
	} else if p.RequireParameters != nil && !*p.RequireParameters {
		resolved.RequireParameters = nil
	}
	if resolved.empty() {
		return nil
	}
	return &resolved
}

func (p *Provider) empty() bool {
	return len(p.Order) == 0 &&
		len(p.Only) == 0 &&
		len(p.Ignore) == 0 &&
		len(p.Quantizations) == 0 &&
		p.Sort == "" &&
		p.DataCollection == "" &&
		p.AllowFallbacks == nil &&
		p.RequireParameters == nil &&
		p.MaxPrice == nil
}
