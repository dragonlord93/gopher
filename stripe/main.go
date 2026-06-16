package main

/*
schedule: {
 "start": "Welcome",
 "duration": -15,
 "end": "Expired"
}

0: [Welcome] username for plan
1: [Update Expiry] username for john
2: [Expired] username for john

{
{name:"John", duration: 30, accout_start_date: 0, plan: Silver}
{name:"John", duration: 30, accout_start_date: 0, plan: Silver}
}

*/
import (
	"fmt"
	"slices"
)

type Notifier struct {
	// 0: [[Welcome] Subscription for John (Silver)]
	emails   map[int][]string
	schedule *Schedule
}

func NewNotifier() *Notifier {
	return &Notifier{
		emails: make(map[int][]string),
	}
}

type Schedule struct {
	start           string
	upcomingExpirty int
	end             string
}

type UserAccounts struct {
	accountDate int
	duration    int
	name        string
	plan        string
}

func (n *Notifier) new(s *Schedule) {
	n.schedule = s
}

func (n *Notifier) sendEmails(userAccounts []UserAccounts) {

	n.emails = make(map[int][]string)
	for _, userAccount := range userAccounts {

		startDate := userAccount.accountDate
		subject := getEmailSubject(userAccount.name, userAccount.plan, "Welcome")
		n.emails[startDate] = append(n.emails[startDate], subject)

		subject = getEmailSubject(userAccount.name, userAccount.plan, "expiry")
		upcomingExpiryKey := userAccount.accountDate + userAccount.duration + n.schedule.upcomingExpirty
		n.emails[upcomingExpiryKey] = append(n.emails[upcomingExpiryKey], subject)

		expiredKey := userAccount.accountDate + userAccount.duration
		subject = getEmailSubject(userAccount.name, userAccount.plan, "expired")
		n.emails[expiredKey] = append(n.emails[expiredKey], subject)
	}
	keySet := make([]int, 0)
	for k, _ := range n.emails {
		keySet = append(keySet, k)
	}
	slices.Sort(keySet)

	for _, v := range keySet {
		for _, val := range n.emails[v] {
			fmt.Println(v, ": ", val)
		}
	}

}

func getEmailSubject(userName string, plan string, emailType string) string {
	subject := "Subscription for " + userName + " (" + plan + ")"
	switch emailType {
	case "Welcome":
		return "[Welcome] " + subject
	case "expiry":
		return "[Upcoming expiry] " + subject
	case "expired":
		return "[Expired] " + subject
	}
	return ""
}

func main() {
	//Enter your code here. Print output to STDOUT

	notifier := &Notifier{}
	notifier.new(&Schedule{
		start:           "Welcome",
		upcomingExpirty: -15,
		end:             "Expired",
	})

	userAccounts := make([]UserAccounts, 0)

	userAccounts = append(userAccounts, UserAccounts{
		accountDate: 0,
		duration:    30,
		name:        "John",
		plan:        "Silver",
	})
	userAccounts = append(userAccounts, UserAccounts{
		accountDate: 1,
		duration:    15,
		name:        "Alice",
		plan:        "Gold",
	})
	notifier.sendEmails(userAccounts)
}
