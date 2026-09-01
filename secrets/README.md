# Local Core agent token

Create `secrets/agent-token.txt` locally before starting Compose. The file is
ignored by Git and is mounted into Core as a Compose secret.

Generate a cryptographically random token without overwriting an existing
file:

```powershell
go run .\cmd\workchronicle-core generate-token .\secrets\agent-token.txt
```

Pass the same file to the Windows Host Agent with `--token-file`.
