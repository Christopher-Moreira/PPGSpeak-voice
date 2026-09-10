# PPG Voice Service

SFU de áudio local do PPG-Speak, escrito em Go com Pion WebRTC v4. Ele recebe
uma faixa Opus de cada participante e encaminha os pacotes RTP para os demais,
sem decodificar ou reencodar áudio.

## Responsabilidades

- signaling WebSocket em `/voice/ws`;
- validação de tickets JWT curtos emitidos pela API principal;
- uma `PeerConnection` por participante;
- rooms em memória e encaminhamento RTP/RTCP;
- eventos de entrada/saída assinados para a API principal;
- health checks e métricas Prometheus.

O MVP é propositalmente **audio-only**. Câmera, compartilhamento de tela,
gravação e escala multi-node não fazem parte desta primeira versão.

## Execução isolada

```bash
export VOICE_TOKEN_SECRET='dev_voice_token_secret_change_me_0123456789'
go run ./cmd/server
```

Normalmente o serviço é iniciado junto da aplicação com `docker compose` na
pasta `deploy/`. O browser usa a rota same-origin `ws://localhost/voice/ws` via
Caddy; a porta `8081` fica exposta em desenvolvimento para diagnóstico.

## Rede

- TCP `8081`: HTTP, health, métricas e signaling WebSocket;
- UDP `50000-50100`: candidatos ICE/mídia do Pion;
- `VOICE_PUBLIC_IP`: configure ao publicar o servidor atrás de NAT;
- `VOICE_STUN_URL` e `VOICE_TURN_URL`: opcionais; localhost/LAN não precisam.

Para Internet pública, configure IP público, TLS/WSS e coturn. O serviço é
single-node: cada room precisa permanecer inteira em uma instância.
