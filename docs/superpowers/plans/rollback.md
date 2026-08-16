# Откат к штатному клиенту NetBird

Оригинальный бандл `/Applications/NetBird.app` **не изменялся** — SIP и защита
App Management не дают править содержимое подписанного приложения, поэтому
пропатченный демон установлен иначе: `/usr/local/bin/netbird` из
`/Library/LaunchDaemons/netbird.plist` переключён с симлинка на бандл на
собственный бинарь `/usr/local/bin/netbird-split`.

## Полный откат

    sudo launchctl unload /Library/LaunchDaemons/netbird.plist
    sudo rm /usr/local/bin/netbird
    sudo ln -s /Applications/NetBird.app/Contents/MacOS/netbird /usr/local/bin/netbird
    sudo launchctl load /Library/LaunchDaemons/netbird.plist

Список split tunnel останется в конфиге профиля, но штатный клиент про поле не
знает и просто его игнорирует, восстанавливая полный туннель.

Файл `/usr/local/bin/netbird-split` можно удалить или оставить — на работу
штатного клиента он не влияет.

## Временное выключение без отката

Не требует прав root и не трогает установку:

    netbird down && netbird up --split-tunnel ""

Обратно:

    netbird down && netbird up --split-tunnel gitlab.example.com,jira.example.com --disable-dns

## Пересборка после обновления NetBird

Обновление официального клиента перезапишет бандл и, возможно, восстановит
симлинк. Тогда нужно пересобрать и переустановить:

    cd /path/to/netbird
    export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"
    git checkout split-tunnel
    go build -o /tmp/netbird-split ./client
    sudo cp /tmp/netbird-split /usr/local/bin/netbird-split
    sudo codesign -f -s - /usr/local/bin/netbird-split
    sudo launchctl unload /Library/LaunchDaemons/netbird.plist
    sudo rm -f /usr/local/bin/netbird
    sudo ln -s /usr/local/bin/netbird-split /usr/local/bin/netbird
    sudo launchctl load /Library/LaunchDaemons/netbird.plist

Если апстрим ушёл вперёд, ветку `split-tunnel` нужно перебазировать на новый
тег; патч намеренно локализован (один новый файл плюс несколько точек
проводки), чтобы это оставалось дешёвым.
