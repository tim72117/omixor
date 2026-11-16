package backend

import "omixor-core/service"

// 組織的系統設定(功能開關):Unsupported、Local(核心 service)、Remote(core/client)。介面宣告在 backend.go。

func (Unsupported) GetSystemSettings(string) (*service.SystemSettingsView, error) {
	return nil, errUnsupported
}
func (Unsupported) SaveSystemSettings(string, service.SystemSettingsInput) (*service.SystemSettingsView, error) {
	return nil, errUnsupported
}

func (l *Local) GetSystemSettings(org string) (*service.SystemSettingsView, error) {
	l.ensureOrg(org)
	return l.Svc.GetSystemSettings(l.P, org)
}

func (l *Local) SaveSystemSettings(org string, in service.SystemSettingsInput) (*service.SystemSettingsView, error) {
	l.ensureOrg(org)
	return l.Svc.SaveSystemSettings(l.P, org, in)
}

func (r *Remote) GetSystemSettings(org string) (*service.SystemSettingsView, error) {
	x, err := r.Client().GetSystemSettings(r.org(org))
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) SaveSystemSettings(org string, in service.SystemSettingsInput) (*service.SystemSettingsView, error) {
	x, err := r.Client().SaveSystemSettings(r.org(org), in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

// ---- 系統層(整台伺服器的功能設定) ----

func (Unsupported) GetServerFeatures() (*service.ServerFeaturesView, error) {
	return nil, errUnsupported
}
func (Unsupported) SaveServerFeatures(service.SystemSettingsInput) (*service.ServerFeaturesView, error) {
	return nil, errUnsupported
}

func (l *Local) GetServerFeatures() (*service.ServerFeaturesView, error) {
	return l.Svc.GetServerFeatures(l.P)
}

func (l *Local) SaveServerFeatures(in service.SystemSettingsInput) (*service.ServerFeaturesView, error) {
	return l.Svc.SaveServerFeatures(l.P, in)
}

func (r *Remote) GetServerFeatures() (*service.ServerFeaturesView, error) {
	x, err := r.Client().GetServerFeatures()
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}

func (r *Remote) SaveServerFeatures(in service.SystemSettingsInput) (*service.ServerFeaturesView, error) {
	x, err := r.Client().SaveServerFeatures(in)
	if err != nil {
		return nil, wrap(err)
	}
	return x, nil
}
