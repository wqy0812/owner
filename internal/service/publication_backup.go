package service

func (p *PublicationBackup) requestPublicationBackup(reason string) {
	if p.publicationBackup != nil {
		p.publicationBackup.Request(reason)
	}
}
