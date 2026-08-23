# STORM 470 — a light bar é o ITE 8233, e o caminho é USB

A Storm 470 tem uma barra de LED no chassi, abaixo do teclado, separada do
backlight por tecla. De fábrica ela fica sempre acesa ciclando cores. Este
documento registra o protocolo, que é agora suportado por
`internal/lightbar/ite8233.go`.

Ele **corrige** duas afirmações anteriores deste repositório:

- `fork-changes.md` dizia *"Lightbar. This model has none."* — tem.
- A conclusão registrada de que o controle real seria a EC (`INOU0000`,
  registradores `0x0748`+) estava errada para esta máquina.

## O dispositivo

```console
$ ls /sys/class/hidraw/*/device/uevent | xargs grep -h HID_NAME
HID_NAME=ITE Tech. Inc. ITE Device(8291)     # 048d:600b — teclado
HID_NAME=ITE Tech. Inc. ITE Device(8233)     # 048d:7001 — light bar
```

O descritor do `048d:7001` é a mesma coleção de fabricante do teclado:

```console
$ od -An -tx1 -v /sys/class/hidraw/hidraw2/device/report_descriptor
 06 03 ff 09 02 a1 01 15 00 26 ff 00 75 08 95 40
 09 20 81 02 09 21 91 02 09 22 95 08 b1 02 c0
```

Usage page `0xFF03`, input 64 B, output 64 B, **feature 8 B, sem report ID**.
Logo vale o mesmo framing já verificado no ITE 8291: `HIDIOCSFEATURE` com um
buffer de **9 bytes**, `[0x00]` seguido dos 8 bytes do pacote, e o kernel
descarta o zero antes de o pacote chegar ao barramento.

## O protocolo

A fonte é `src/ite_8291_lb/ite_8291_lb.c` do `tuxedo-drivers`, cuja tabela HID
lista `048d:6010`, `048d:7000` e `048d:7001` — a nossa. Dois comandos:

| Comando | Pacote |
|---|---|
| Cor | `0x14, cvar, slot, R, G, B, 0x00, 0x00` |
| Modo | `0x08, mvar, mode, speed, brightness, apply, dir, 0x00` |

`cvar`/`mvar` são **variantes por produto**, e é aí que mora o perigo:

| Produto | `cvar` | `mvar` |
|---|---|---|
| `0x6010` | `0x00` | `0x02` |
| `0x7000` | `0x01` | `0x21` |
| `0x7001` | `0x00` | `0x22` |

`slot` vai de 1 a 7: o slot 1 é a cor do modo estático, e os sete juntos formam
a lista que as animações percorrem. `brightness` é `0x00`–`0x64`. `speed` é
`0x01` (mais rápido) a `0x0A` (mais lento).

Modos, todos **verificados no hardware** em 2026-08-22:

| Modo | Código | `apply` |
|---|---|---|
| static (direct) | `0x01` | `0x01` |
| breathing | `0x02` | `0x08` |
| wave | `0x03` | `0x01` |
| bounce | `0x04` | `0x08` |
| marquee | `0x05` | `0x01` |
| scan | `0x06` | `0x01` |

Isso vai além do estado da arte publicado: a TUXEDO só implementou `breathing`,
`wave`, `clash`, `catchup` e `flash` para os irmãos `0x6010`/`0x7000`, deixando
o `0x7001` só com cor sólida, e o [keyRGB](https://github.com/Rainexn0b/keyRGB)
marca explicitamente os `apply` do `0x7001` como desconhecidos. Os seis modos
acima funcionam, e o modo estático cancela qualquer animação em andamento.

## O erro que apagou a barra por uma sessão inteira

Uma tentativa anterior mandou `[0x08, 0x01]` — o "off" do ITE **8291**, o
teclado — para o `/dev/hidraw2`. A barra apagou e nada a trouxe de volta: nem
outros valores de effect id, nem `avellcc reload`, nem a EC, nem um reboot.

A explicação está na tabela acima. `0x08` é o comando de modo; o byte seguinte
tem de ser `0x22` neste MCU. `0x01` é a variante do **`0x6010`**, e
`{0x08, 0x01, 0x00, ...}` é literalmente o terceiro estágio da sequência de
desligar daquele produto. O pacote foi aceito, `ioctl` retornou sucesso, e a
barra entrou em `MODE_OFF`.

O que a destravou foi o primeiro pacote com a variante certa. Ou seja: **não era
um latch de energia** — daí o reboot não resolver — **era um modo, e só um
comando de modo endereçado corretamente podia sair dele**. A varredura anterior
testou "todos os 16 valores de control byte", `0x00`–`0x0F`, e por isso nunca
alcançou `0x22`.

Duas lições que valem para qualquer MCU deste tipo:

1. **Sucesso do `ioctl` não é sinal de nada.** Este controlador aceita a
   variante errada e a interpreta como outro comando. Um teste sem observação
   visual não distingue "não fez nada" de "fez outra coisa".
2. **Um espaço de busca precisa ser justificado, não arredondado.** `0x00`–`0x0F`
   parece exaustivo e não é; a resposta estava em `0x22`.

## `--off`

`ITE8233.Off()` escreve preto no modo estático com brilho zero, e
**deliberadamente não** usa a sequência de quatro estágios do fabricante
(`0x12` → `0x08 0x05` → `0x08 0x01` → `0x1A`). Aquela sequência leva a barra
exatamente ao estado descrito acima. Ele é recuperável agora que a variante é
conhecida, mas preto no modo direto sai com qualquer escrita de cor posterior —
que é a propriedade que interessa em uma CLI.

## A EC não é o caminho aqui

`EC_ADDR_LIGHTBAR_AC_CTRL = 0x0748` e vizinhos existem de verdade no driver
mainline `uniwill-laptop`, e o `INOU0000` desta máquina é EC real
(ver `storm470-ec-inou.md`). Mas eles controlam a light bar das máquinas Uniwill
que **não** têm este MCU USB. Foi por isso que, com a barra apagada,
`rainbow_animation` continuava lendo `1`: o `048d:7001` ganha da EC.

Consequência prática: **não é preciso `uniwill-laptop force=1` nem instalar o
`tuxedo-drivers`** para controlar a barra — a ADR 0006 continua valendo intacta.

## Uso

```bash
avellcc lightbar                                        # status e opções
avellcc lightbar --color '#00ff00' --brightness 40      # estático
avellcc lightbar --brightness 100                       # muda só o brilho
avellcc lightbar --effect wave --speed 3                # arco-íris de fábrica
avellcc lightbar --effect breathing --color purple      # animação de uma cor só
avellcc lightbar --off
```

O controlador não tem leitura de estado — o `GET_FEATURE` responde tudo zero
independentemente do que a barra esteja fazendo — então o status mostra o que o
`avellcc` escreveu por último, e diz isso.
