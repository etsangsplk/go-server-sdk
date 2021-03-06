package ldclient

import "gopkg.in/launchdarkly/go-sdk-common.v1/ldvalue"

// NewUser creates a new user identified by the given key.
func NewUser(key string) User {
	return User{Key: &key}
}

// NewAnonymousUser creates a new anonymous user identified by the given key.
func NewAnonymousUser(key string) User {
	anonymous := true
	return User{Key: &key, Anonymous: &anonymous}
}

// UserBuilder is a mutable object that uses the Builder pattern to specify properties for a User.
// This is the preferred method for constructing a User; direct access to User fields will be
// removed in a future version.
//
// Obtain an instance of UserBuilder by calling NewUserBuilder, then call setter methods such as
// Name to specify any additional user properties, then call Build() to construct the User. All of
// the UserBuilder setters return a reference the same builder, so they can be chained together:
//
//     user := NewUserBuilder("user-key").Name("Bob").Email("test@example.com").Build()
//
// Setters for user attributes that can be designated private return the type
// UserBuilderCanMakeAttributePrivate, so you can chain the AsPrivateAttribute method:
//
//     user := NewUserBuilder("user-key").Name("Bob").AsPrivateAttribute().Build() // Name is now private
//
// A UserBuilder should not be accessed by multiple goroutines at once.
type UserBuilder interface {
	// Key changes the unique key for the user being built.
	Key(value string) UserBuilder

	// Secondary sets the secondary key attribute for the user being built.
	//
	// This affects feature flag targeting (https://docs.launchdarkly.com/docs/targeting-users#section-targeting-rules-based-on-user-attributes)
	// as follows: if you have chosen to bucket users by a specific attribute, the secondary key (if set)
	// is used to further distinguish between users who are otherwise identical according to that attribute.
	Secondary(value string) UserBuilderCanMakeAttributePrivate

	// IP sets the IP address attribute for the user being built.
	IP(value string) UserBuilderCanMakeAttributePrivate

	// Country sets the country attribute for the user being built.
	Country(value string) UserBuilderCanMakeAttributePrivate

	// Email sets the email attribute for the user being built.
	Email(value string) UserBuilderCanMakeAttributePrivate

	// FirstName sets the first name attribute for the user being built.
	FirstName(value string) UserBuilderCanMakeAttributePrivate

	// LastName sets the last name attribute for the user being built.
	LastName(value string) UserBuilderCanMakeAttributePrivate

	// Avatar sets the avatar URL attribute for the user being built.
	Avatar(value string) UserBuilderCanMakeAttributePrivate

	// Name sets the full name attribute for the user being built.
	Name(value string) UserBuilderCanMakeAttributePrivate

	// Anonymous sets the anonymous attribute for the user being built.
	//
	// If a user is anonymous, the user key will not appear on your LaunchDarkly dashboard.
	Anonymous(value bool) UserBuilder

	// Custom sets a custom attribute for the user being built.
	//
	//     user := NewUserBuilder("user-key").
	//         Custom("custom-attr-name", ldvalue.String("some-string-value")).AsPrivateAttribute().
	//         Build()
	Custom(name string, value ldvalue.Value) UserBuilderCanMakeAttributePrivate

	// Build creates a User from the current UserBuilder properties.
	//
	// The User is independent of the UserBuilder once you have called Build(); modifying the UserBuilder
	// will not affect an already-created User.
	Build() User
}

// UserBuilderCanMakeAttributePrivate is an extension of UserBuilder that allows attributes to be
// made private via the AsPrivateAttribute() method. All UserBuilderCanMakeAttributePrivate setter
// methods are the same as UserBuilder, and apply to the original builder.
//
// UserBuilder setter methods for attributes that can be made private always return this interface.
// See AsPrivateAttribute for details.
type UserBuilderCanMakeAttributePrivate interface {
	UserBuilder

	// AsPrivateAttribute marks the last attribute that was set on this builder as being a private attribute: that is, its
	// value will not be sent to LaunchDarkly.
	//
	// This action only affects analytics events that are generated by this particular user object. To mark some (or all)
	// user attributes as private for all users, use the Config properties PrivateAttributeName and AllAttributesPrivate.
	//
	// Most attributes can be made private, but Key and Anonymous cannot. This is enforced by the compiler, since the builder
	// methods for attributes that can be made private are the only ones that return UserBuilderCanMakeAttributePrivate;
	// therefore, you cannot write an expression like NewUserBuilder("user-key").AsPrivateAttribute().
	//
	// In this example, FirstName and LastName are marked as private, but Country is not:
	//
	//     user := NewUserBuilder("user-key").
	//         FirstName("Pierre").AsPrivateAttribute().
	//         LastName("Menard").AsPrivateAttribute().
	//         Country("ES").
	//         Build()
	AsPrivateAttribute() UserBuilder

	// AsNonPrivateAttribute marks the last attribute that was set on this builder as not being a private attribute:
	// that is, its value will be sent to LaunchDarkly and can appear on the dashboard.
	//
	// This is the opposite of AsPrivateAttribute(), and has no effect unless you have previously called
	// AsPrivateAttribute() for the same attribute on the same user builder. For more details, see
	// AsPrivateAttribute().
	AsNonPrivateAttribute() UserBuilder
}

type userBuilderImpl struct {
	key          string
	secondary    ldvalue.OptionalString
	ip           ldvalue.OptionalString
	country      ldvalue.OptionalString
	email        ldvalue.OptionalString
	firstName    ldvalue.OptionalString
	lastName     ldvalue.OptionalString
	avatar       ldvalue.OptionalString
	name         ldvalue.OptionalString
	anonymous    bool
	hasAnonymous bool
	custom       map[string]interface{}
	privateAttrs map[string]bool
}

type userBuilderCanMakeAttributePrivate struct {
	builder  *userBuilderImpl
	attrName string
}

// NewUserBuilder constructs a new UserBuilder, specifying the user key.
//
// For authenticated users, the key may be a username or e-mail address. For anonymous users,
// this could be an IP address or session ID.
func NewUserBuilder(key string) UserBuilder {
	return &userBuilderImpl{key: key}
}

// NewUserBuilderFromUser constructs a new UserBuilder, copying all attributes from an existing user. You may
// then call setter methods on the new UserBuilder to modify those attributes.
func NewUserBuilderFromUser(fromUser User) UserBuilder {
	builder := &userBuilderImpl{
		secondary: fromUser.GetSecondaryKey(),
		ip:        fromUser.GetIP(),
		country:   fromUser.GetCountry(),
		email:     fromUser.GetEmail(),
		firstName: fromUser.GetFirstName(),
		lastName:  fromUser.GetLastName(),
		avatar:    fromUser.GetAvatar(),
		name:      fromUser.GetName(),
	}
	if fromUser.Key != nil {
		builder.key = *fromUser.Key
	}
	if fromUser.Anonymous != nil {
		builder.anonymous = *fromUser.Anonymous
		builder.hasAnonymous = true
	}
	if fromUser.Custom != nil {
		builder.custom = make(map[string]interface{}, len(*fromUser.Custom))
		for k, v := range *fromUser.Custom {
			builder.custom[k] = v
		}
	}
	if len(fromUser.PrivateAttributeNames) > 0 {
		builder.privateAttrs = make(map[string]bool, len(fromUser.PrivateAttributeNames))
		for _, name := range fromUser.PrivateAttributeNames {
			builder.privateAttrs[name] = true
		}
	}
	return builder
}

func (b *userBuilderImpl) canMakeAttributePrivate(attrName string) UserBuilderCanMakeAttributePrivate {
	return &userBuilderCanMakeAttributePrivate{builder: b, attrName: attrName}
}

func (b *userBuilderImpl) Key(value string) UserBuilder {
	b.key = value
	return b
}

func (b *userBuilderImpl) Secondary(value string) UserBuilderCanMakeAttributePrivate {
	b.secondary = ldvalue.NewOptionalString(value)
	return b.canMakeAttributePrivate("secondary")
}

func (b *userBuilderImpl) IP(value string) UserBuilderCanMakeAttributePrivate {
	b.ip = ldvalue.NewOptionalString(value)
	return b.canMakeAttributePrivate("ip")
}

func (b *userBuilderImpl) Country(value string) UserBuilderCanMakeAttributePrivate {
	b.country = ldvalue.NewOptionalString(value)
	return b.canMakeAttributePrivate("country")
}

func (b *userBuilderImpl) Email(value string) UserBuilderCanMakeAttributePrivate {
	b.email = ldvalue.NewOptionalString(value)
	return b.canMakeAttributePrivate("email")
}

func (b *userBuilderImpl) FirstName(value string) UserBuilderCanMakeAttributePrivate {
	b.firstName = ldvalue.NewOptionalString(value)
	return b.canMakeAttributePrivate("firstName")
}

func (b *userBuilderImpl) LastName(value string) UserBuilderCanMakeAttributePrivate {
	b.lastName = ldvalue.NewOptionalString(value)
	return b.canMakeAttributePrivate("lastName")
}

func (b *userBuilderImpl) Avatar(value string) UserBuilderCanMakeAttributePrivate {
	b.avatar = ldvalue.NewOptionalString(value)
	return b.canMakeAttributePrivate("avatar")
}

func (b *userBuilderImpl) Name(value string) UserBuilderCanMakeAttributePrivate {
	b.name = ldvalue.NewOptionalString(value)
	return b.canMakeAttributePrivate("name")
}

func (b *userBuilderImpl) Anonymous(value bool) UserBuilder {
	b.anonymous = value
	b.hasAnonymous = true
	return b
}

func (b *userBuilderImpl) Custom(name string, value ldvalue.Value) UserBuilderCanMakeAttributePrivate {
	if b.custom == nil {
		b.custom = make(map[string]interface{})
	}
	// Note: since User.Custom is currently exported, and existing application code may expect to
	// see only basic Go types in that map rather than ldvalue.Value instances, we are using a
	// method that converts ldvalue.Value to a raw bool, string, map, etc. If it is a slice or a
	// map, then it is mutable, which is undesirable; this is why direct access to User.Custom is
	// deprecated. In a future version when backward compatibility is no longer an issue, a custom
	// attribute will be stored as a completely immutable Value.
	b.custom[name] = value.UnsafeArbitraryValue() //nolint // allow deprecated usage
	return b.canMakeAttributePrivate(name)
}

func (b *userBuilderImpl) Build() User {
	key := b.key
	u := User{
		Key:       &key,
		Secondary: b.secondary.AsPointer(),
		Ip:        b.ip.AsPointer(),
		Country:   b.country.AsPointer(),
		Email:     b.email.AsPointer(),
		FirstName: b.firstName.AsPointer(),
		LastName:  b.lastName.AsPointer(),
		Avatar:    b.avatar.AsPointer(),
		Name:      b.name.AsPointer(),
	}
	if b.hasAnonymous {
		value := b.anonymous
		u.Anonymous = &value
	}
	if len(b.custom) > 0 {
		c := make(map[string]interface{}, len(b.custom))
		for k, v := range b.custom {
			c[k] = v
		}
		u.Custom = &c
	}
	if len(b.privateAttrs) > 0 {
		a := make([]string, 0, len(b.privateAttrs))
		for key, value := range b.privateAttrs {
			if value {
				a = append(a, key)
			}
		}
		u.PrivateAttributeNames = a
	}
	return u
}

func (b *userBuilderCanMakeAttributePrivate) AsPrivateAttribute() UserBuilder {
	if b.builder.privateAttrs == nil {
		b.builder.privateAttrs = make(map[string]bool)
	}
	b.builder.privateAttrs[b.attrName] = true
	return b.builder
}

func (b *userBuilderCanMakeAttributePrivate) AsNonPrivateAttribute() UserBuilder {
	if b.builder.privateAttrs != nil {
		delete(b.builder.privateAttrs, b.attrName)
	}
	return b.builder
}

func (b *userBuilderCanMakeAttributePrivate) Key(value string) UserBuilder {
	return b.builder.Key(value)
}

func (b *userBuilderCanMakeAttributePrivate) Secondary(value string) UserBuilderCanMakeAttributePrivate {
	return b.builder.Secondary(value)
}

func (b *userBuilderCanMakeAttributePrivate) IP(value string) UserBuilderCanMakeAttributePrivate {
	return b.builder.IP(value)
}

func (b *userBuilderCanMakeAttributePrivate) Country(value string) UserBuilderCanMakeAttributePrivate {
	return b.builder.Country(value)
}

func (b *userBuilderCanMakeAttributePrivate) Email(value string) UserBuilderCanMakeAttributePrivate {
	return b.builder.Email(value)
}

func (b *userBuilderCanMakeAttributePrivate) FirstName(value string) UserBuilderCanMakeAttributePrivate {
	return b.builder.FirstName(value)
}

func (b *userBuilderCanMakeAttributePrivate) LastName(value string) UserBuilderCanMakeAttributePrivate {
	return b.builder.LastName(value)
}

func (b *userBuilderCanMakeAttributePrivate) Avatar(value string) UserBuilderCanMakeAttributePrivate {
	return b.builder.Avatar(value)
}

func (b *userBuilderCanMakeAttributePrivate) Name(value string) UserBuilderCanMakeAttributePrivate {
	return b.builder.Name(value)
}

func (b *userBuilderCanMakeAttributePrivate) Anonymous(value bool) UserBuilder {
	return b.builder.Anonymous(value)
}

func (b *userBuilderCanMakeAttributePrivate) Custom(name string, value ldvalue.Value) UserBuilderCanMakeAttributePrivate {
	return b.builder.Custom(name, value)
}

func (b *userBuilderCanMakeAttributePrivate) Build() User {
	return b.builder.Build()
}
