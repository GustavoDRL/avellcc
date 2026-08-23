# STORM 470 — o `INOU0000` é EC RAM real, ao contrário do `AMW0`

Complemento a `storm470-fans.md`. Aquele documento provou que os GUIDs WMI
`ABBC0F6A`–`72` sob `\_SB.AMW0` são o código de exemplo WMI-ACPI da AMI. Esta
nota registra um achado **oposto e igualmente importante**: existe no mesmo
firmware uma interface de EC que **é** real, e ela não é WMI.

## O dispositivo

```asl
Device (INOU)
{
    Name (_HID, "INOU0000")
    Name (_UID, Zero)
    Mutex (UWOL, 0x00)
    Name (_CRS, ResourceTemplate ()
    {
        Memory32Fixed (ReadWrite,
            0xFE410000,         // Address Base
            0x00001000,         // Address Length
            )
    })
    Method (_STA, 0, NotSerialized) { Return (0x0B) }
```

`_STA = 0x0B` significa presente, habilitado e funcional — só o bit "mostrar na
UI" está desligado. É uma janela de **4 KB de RAM da EC mapeada em MMIO** em
`0xFE410000`.

## O acessador é genuíno

```asl
Method (ECRR, 1, NotSerialized)          // ler byte
{
    Local0 = (0xFE410000 + Arg0)
    Local1 = MMRW (Local0, Zero, Zero, Zero)
    Return (Local1)
}

Method (ECRW, 2, NotSerialized)          // escrever byte
{
    Local0 = (0xFE410000 + Arg0)
    MMRW (Local0, One, Zero, Arg1)
}

Method (MMRW, 4, NotSerialized)
{
    Acquire (UWOL, 0xFFFF)
    OperationRegion (MMNM, SystemMemory, Arg0, 0x04)
    Field (MMNM, ByteAcc, NoLock, Preserve) { MM08, 8 }
    ...
}
```

`OperationRegion (..., SystemMemory, ...)` + `Field` é acesso de memória de
verdade, protegido por mutex. Não retorna buffer canned. Compare com o `GETB` do
`AMW0`, que devolve a string literal `"ABCDEFGH…"`.

## Por que isso importa (e por que não muda a decisão)

O driver mainline `uniwill-laptop` (`drivers/platform/x86/uniwill/`, kernel 7.1)
fala com o hardware exatamente por aqui: seus símbolos `uniwill_ec_reg_read` /
`uniwill_ec_reg_write` chamam `ECRR`/`ECRW` e montam um `regmap` em cima. O
device `INOU0000:00` existe nesta máquina e está **sem driver**, porque a tabela
DMI do módulo cobre só `TUXEDO*`, `SchenkerTechnologiesGmbH` e
`Intel(R) Client Systems`.

Isso refina — sem contradizer — a alternativa rejeitada na ADR 0006
("`uniwill-laptop force=1` … forçar é chute em EC desconhecido"). O que é chute
é o **layout dos registradores**, não o mecanismo de acesso. São coisas
separadas, e confundi-las leva à conclusão errada nas duas direções.

Vale registrar também o que o upstream faz com `force`, porque é contraintuitivo:

```c
if (force) {
    /* Assume that the device supports all features except the charge limit */
    device_descriptor.features = UINT_MAX & ~UNIWILL_FEATURE_BATTERY_CHARGE_LIMIT;
}
```

A documentação do kernel explica: *"Some devices do not properly implement the
charging threshold interface. Forcing the driver to enable access to said
interface on such devices might damage the battery."* Ou seja, `force=1` **nunca**
entrega `charge_control_end_threshold` — entrega `charge_types`
(`Standard` / `Long Life` / `Trickle`), e liga junto CTGP e lightbar, que
escrevem em offsets não verificados aqui.

## O que aconteceu quando o driver foi carregado

O `force=1` foi testado no mesmo dia e **funcionou**: `hwmon` `uniwill` com
`fan1_input`/`fan2_input` em RPM e `temp1`/`temp2` (CPU/GPU), mais a lightbar e
os knobs de plataforma. As leituras são genuínas, não eco da entrada — `temp1`
bateu com `coretemp` e `acpitz` no mesmo instante, acompanhando 86→94 °C sob
carga real. Detalhes em `storm470-fans.md` e na ADR 0008.

Isso confirma a tese desta nota: o `INOU0000` é substrato real, ao contrário do
`AMW0`.

**Mas o perfil de carga não veio, e esse é o resultado negativo que importa.**
A documentação do kernel promete `charge_types` no lugar do threshold cru quando
se usa `force`. Não apareceu:

```console
$ lsmod | grep -c uniwill_laptop
1
$ ls /sys/class/power_supply/BAT0/extensions/
$ ls /sys/class/power_supply/BAT0/ | grep charge
charge_full
charge_full_design
charge_now
```

`extensions/` vazio significa que a *power supply extension* não se registrou —
nem o caminho seguro de perfil de carga existe nesta EC. Portanto a decisão de
não perseguir limite de carga por software está agora **verificada**, não apenas
inferida da leitura do código.

O controle de energia ficou com RAPL, que não toca a EC — ver a ADR 0007 em
`~/Work/docs/decisoes/`. Esta nota existe para que quem ler "os GUIDs são falsos"
não conclua que *todo* o firmware é falso, e para que, se um dia alguém mapear os
offsets da EC, saiba que a janela está ali, documentada e endereçável.

## Como verificar

```console
$ cat /sys/bus/acpi/devices/INOU0000:00/status
11
$ ls /sys/devices/platform/INOU0000:00/
driver_override  firmware_node  modalias  power  subsystem  uevent  waiting_for_supplier
$ ls /sys/class/power_supply/BAT0/extensions/
$ modinfo uniwill-laptop | grep -c '^alias.*TUXEDO'
50
```

O `extensions/` vazio é o gancho onde a *power supply extension* do driver se
registraria. `driver_override` sem symlink `driver` confirma que ninguém
reivindicou o device.
