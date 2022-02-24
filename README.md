goCrawler
=========

Text-mining topic relevant sources
----------------------------------

- [About goCrawler](#About-goCrawler)
- [Install](#install)
- [Run](#run)
- [Output](#output)
- [About the author](#About-the-author)
- [Redis Install](#Redis-Install)

#### About goCrawler

goLang crawler restricted to only topic relevant/curated URLs. It includes token frequency analysis and NLP nGram detection

Watch goLang goCrawler running at https://youtu.be/7IDkNYcMXxU


#### Install

By using go.mod needed libraries are automagically downloaded on first run. Add Redis service. Check this document at the end of it.


#### Run

Create myTopic.yaml using covid-19.yaml or blockchain.yaml as template

```shell
    $ go run crawler.go <myTopic>
```

neutral.yaml is a non-topic crawling profile to get a non-topic english corpus

```shell
    $ go run crawler.go neutral
```


#### Output

From time to time, you will get an on-screen summary of relevant kwywords found so far like this:

```text
    numLinksProcessed: 150

    Corpus frequencies:  [{the 5191} {note 637} {convoy 503} {ottawa 426} {canada 395} {police 336} {covid 327} {canadian 325} {protest 277} {truckers 244} {protesters 243} {vaccine 231} {freedom 190} {government 183} {protests 181} {border 172} {mandates 170} {trudeau 134} {health 130} {vaccinated 127} {right 115} {city 114} {trucks 113} {ontario 111} {restrictions 109} {vehicles 108} {minister 106} {truck 105} {retrieved 100} {toronto 99} {media 97} {last 96} {gofundme 95} {country 91} {pandemic 89} {organizers 88} {end 88} {downtown 88} {vaccination 86} {federal 85} {canadians 83} {far 82} {parliament 81} {called 80} {global 78} {united 76} {party 76} {drivers 75} {cross 75} {several 74} {cbc 74} {service 73} {states 72} {measures 72} {mandate 72} {statement 71} {prime 70} {conservative 70} {act 70} {supporters 69} {justin 69} {movement 66} {groups 64} {hill 63} {street 62} {law 62} {traffic 61} {friday 60} {down 60} {trucking 59} {president 59} {reported 58} {funds 58} {fully 58} {bridge 58} {capital 57} {sunday 56} {saturday 56} {demonstrators 56} {trucker 55} {political 55} {king 55} {highway 55} {press 54} {monday 54} {jan 54} {began 54} {occupation 53} {trump 52} {court 52} {area 52} {provincial 51} {saying 50} {members 50} {emergency 50} {workers 49} {security 49} {off 49} {chief 49} {american 49}]


    Corpus frequencies w/o Eng.:  [{convoy 499} {ottawa 414} {canadian 294} {canada 258} {protest 253} {truckers 244} {protesters 238} {note 219} {vaccine 216} {mandates 168} {protests 160} {trudeau 134} {vaccinated 127} {covid 116} {trucks 107} {freedom 103} {gofundme 95} {retrieved 92} {truck 89} {vaccination 83} {ontario 81} {organizers 80} {canadians 80} {downtown 79} {vehicles 76} {cbc 74} {police 70} {mandate 68} {trucking 59} {trucker 55} {toronto 53} {justin 52} {demonstrators 50} {highway 46} {cent 41} {unvaccinated 40} {rcmp 40} {blockade 40} {windsor 37} {supporters 36} {organizer 36} {ctv 36} {postmedia 35} {lich 35} {givesendgo 34} {emergencies 34} {blockades 33} {provincial 31} {occupation 31} {alberta 31} {sloly 30} {protestors 30} {crowdfunding 30} {protesting 29} {conservative 29} {jan 26} {vancouver 23} {vaccines 23} {tamara 23} {feb 23} {ukraine 22} {omicron 22} {crossings 22} {tweeted 21} {provinces 21} {horns 21} {toole 20} {rally 20} {nova 19} {donations 19} {scotia 18} {pressprogress 18} {ops 18} {crossing 18} {convoys 18} {confederate 18} {passports 17} {ndp 17} {bergen 17} {variant 16} {saskatchewan 16} {opp 16} {occupiers 16} {mou 16} {invoked 16} {fundraiser 16} {demonstrations 16} {coutts 16} {arrests 16} {peaceful 15} {zello 14} {watson 14} {invocation 14} {insurrection 14} {hospitalized 14} {honking 14} {freeland 14} {extremists 14} {drivers 14} {bloor 14}]
```

You may find topic relevant text at logs/corpusCuratedText.log

Other interesting results produced by computerphysicslab/goCrawler:
- Most frequent nGrams for COVID-19.txt: https://gist.github.com/computerphysicslab/49c6366f501c81a36a2cd15e25c3d386
- 500 covid-19 keywords frequencies.txt: https://gist.github.com/computerphysicslab/e3fbbf6d8b3e5be9048f70f872467617
- 500 best COVID-19 relevant links.md: https://gist.github.com/computerphysicslab/86ca73169279f9af24d9739d53ed4e35


#### About the author

Find me at https://www.linkedin.com/in/semanticwebarchitect/ or computerphysicslab@gmail.com

I am very interested on Text-Mining, Natural Language Processing, Named Entity Recognition and building textual corpora and search apps for Health/Biomedical/Clinical domain


#### Redis Install

Appendix: How to install Redis service on Linux Ubuntu

goCrwaler uses Redis noSQL local database to cache visited urls and avoid unnecessary network overload. So you need to install it prior to running the crawler.

"Redis is very fast and can perform about 110000 SETs per second, about 81000 GETs per second."

https://www.tutorialspoint.com/redis/redis_overview.htm

#redis #nosql #performance #benchmarks

```shell
sudo apt-get update
sudo apt-get install redis-server

redis-server
	770156:C 03 Sep 2020 08:11:43.794 # oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo
	770156:C 03 Sep 2020 08:11:43.794 # Redis version=5.0.7, bits=64, commit=00000000, modified=0, pid=770156, just started
	770156:C 03 Sep 2020 08:11:43.794 # Warning: no config file specified, using the default config. In order to specify a config file use redis-server /path/to/redis.conf
	770156:M 03 Sep 2020 08:11:43.795 * Increased maximum number of open files to 10032 (it was originally set to 1024).
	770156:M 03 Sep 2020 08:11:43.796 # Could not create server TCP listening socket *:6379: bind: Address already in use

redis-cli
redis 127.0.0.1:6379>
redis 127.0.0.1:6379> ping
	PONG

sudo /etc/init.d/redis-server restart
sudo /etc/init.d/redis-server stop
sudo /etc/init.d/redis-server start

	772459:C 03 Sep 2020 08:22:37.635 # oO0OoO0OoO0Oo Redis is starting oO0OoO0OoO0Oo
	772459:C 03 Sep 2020 08:22:37.635 # Redis version=5.0.7, bits=64, commit=00000000, modified=0, pid=772459, just started
	772459:C 03 Sep 2020 08:22:37.635 # Warning: no config file specified, using the default config. In order to specify a config file use redis-server /path/to/redis.conf
	772459:M 03 Sep 2020 08:22:37.637 * Increased maximum number of open files to 10032 (it was originally set to 1024).
					_._                                                  
			_.-``__ ''-._                                             
		_.-``    `.  `_.  ''-._           Redis 5.0.7 (00000000/0) 64 bit
	.-`` .-```.  ```\/    _.,_ ''-._                                   
	(    '      ,       .-`  | `,    )     Running in standalone mode
	|`-._`-...-` __...-.``-._|'` _.-'|     Port: 6379
	|    `-._   `._    /     _.-'    |     PID: 772459
	`-._    `-._  `-./  _.-'    _.-'                                   
	|`-._`-._    `-.__.-'    _.-'_.-'|                                  
	|    `-._`-._        _.-'_.-'    |           http://redis.io        
	`-._    `-._`-.__.-'_.-'    _.-'                                   
	|`-._`-._    `-.__.-'    _.-'_.-'|                                  
	|    `-._`-._        _.-'_.-'    |                                  
	`-._    `-._`-.__.-'_.-'    _.-'                                   
		`-._    `-.__.-'    _.-'                                       
			`-._        _.-'                                           
				`-.__.-'                                               

	772459:M 03 Sep 2020 08:22:37.639 # Server initialized
    ...
	772459:M 03 Sep 2020 08:22:37.639 * Ready to accept connections
```

Checking Redis server is an active system process:

```shell
ps aux | grep redis
	redis     653361  0.1  0.0  55332  5048 ?        Ssl  18:11   0:00 /usr/bin/redis-server 127.0.0.1:6379
```
