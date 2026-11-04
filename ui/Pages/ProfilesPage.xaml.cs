using System;
using System.Windows;
using System.Windows.Controls;

namespace NoMoreDPI.Pages;

public partial class ProfilesPage : Page
{
    readonly AppState state = AppState.Current;

    public ProfilesPage()
    {
        InitializeComponent();
        DataContext = state;
    }

    void Add_Click(object sender, RoutedEventArgs e) => state.Config?.AddProfile();

    async void Delete_Click(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.DataContext is not ProfileModel p || state.Config == null) return;
        if (await Dialogs.ConfirmAsync("Delete profile", $"Delete the \"{p.Name}\" profile?", "Delete"))
            state.Config.Remove(p);
    }

    async void Find_Click(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.DataContext is not ProfileModel p) return;
        if (state.Dirty)
        {
            Dialogs.Error("Save your changes first.");
            return;
        }
        try { await state.AutoFindAsync(p); }
        catch (Exception ex) { Dialogs.Error(ex.Message); }
    }
}
