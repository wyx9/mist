hello:
	@echo "hello world"

# generate proto code: delegate to scripts/gen_proto.ps1 (auto tools + clean + generate)
pt:
	@echo "generate proto..."
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gen_proto.ps1

# generate config tables: excel -> Go code + json data
configs:
	@echo "generate configs..."
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gen_configs.ps1

configs-push:
	@echo "generate configs and push to redis..."
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/gen_configs.ps1 -Push
