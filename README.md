# PPG Voice Service

SFU local do PPG-Speak, escrito em Go com Pion WebRTC v4. Ele recebe microfone,
câmera, compartilhamento de tela e áudio opcional da apresentação, encaminhando
os pacotes RTP aos demais participantes sem decodificar ou reencodar a mídia.

## Responsabilidades

- signaling WebSocket em `/voice/ws`;
- validação de tickets JWT curtos emitidos pela API principal;
- uma `PeerConnection` por participante;
- slots de publicação para microfone, câmera, tela e áudio da tela;
- rooms em memória e encaminhamento RTP/RTCP, incluindo pedidos de keyframe PLI;
- eventos de entrada/saída assinados para a API principal;
- health checks e métricas Prometheus.

Áudio usa Opus. Vídeo negocia os codecs WebRTC disponíveis entre browser e
Pion (VP8/H.264/VP9/AV1 conforme suporte). Gravação, simulcast/SVC, controle de
assinatura por qualidade e escala multi-node ainda não fazem parte desta versão.

## Execução isolada

```bash
export VOICE_TOKEN_SECRET='dev_voice_token_secret_change_me_0123456789'
go run ./cmd/server
```

Normalmente o serviço é iniciado junto da aplicação com `docker compose` na
pasta `deploy/`. O browser usa a rota same-origin `ws://localhost/voice/ws` via
Caddy; a porta `8081` fica exposta em desenvolvimento para diagnóstico.

Com a stack em execução, o smoke test cria dois peers sintéticos e confirma o
encaminhamento de microfone Opus, câmera VP8, tela VP8 e áudio Opus da
apresentação nos dois sentidos:

```bash
cd deploy
docker compose exec voice go run ./cmd/smoke --url ws://127.0.0.1:8081/voice/ws
```

## Rede

- TCP `8081`: HTTP, health, métricas e signaling WebSocket;
- UDP `50000-50100`: candidatos ICE/mídia do Pion;
- TCP `50000`: ICE-TCP passivo, usado pelo TCP Proxy da Railway;
- `VOICE_PUBLIC_IP`: configure ao publicar o servidor atrás de NAT;
- `VOICE_STUN_URL` e `VOICE_TURN_URL`: opcionais; localhost/LAN não precisam.

Na Railway, configure `VOICE_ICE_TCP_PORT=50000` e
`VOICE_ICE_TCP_ONLY=true`, crie um TCP Proxy nessa porta e deixe as variáveis
injetadas `RAILWAY_TCP_PROXY_DOMAIN`/`RAILWAY_TCP_PROXY_PORT` anunciarem o
endpoint público. Em um host com UDP público, use `VOICE_PUBLIC_IP`, TLS/WSS e
coturn. O serviço é single-node: cada room precisa permanecer inteira em uma
instância.
